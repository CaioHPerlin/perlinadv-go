# PerlinAdv (API Go)

Deploy com Traefik, GHCR e GitHub Actions: [guia de produção](doc/deployment.md).

API de monitoramento DJEN e gestão de processos e prazos. O resultado da extração e do cálculo é **sugestão**: nenhum prazo calculado é confirmado ou enviado ao Google Agenda sem ação do advogado. A especificação HTTP está em [doc/openapi.yaml](doc/openapi.yaml).

## Executar localmente

Requisitos: Docker com Compose. Copie `.env.example` para `.env` e troque `POSTGRES_PASSWORD`. Para executar API e worker, rode:

```sh
docker compose up --build -d
docker compose ps
curl http://localhost:3000/health
```

Serviços locais: API `localhost:3000`, PostgreSQL `localhost:5432`, caixa de e-mails de desenvolvimento em `localhost:8025`. O serviço `migrate` aplica as migrações antes de iniciar API e worker. O worker consulta a API oficial por OAB e tribunal a cada hora: verifica hoje, reconcilia os sete dias úteis anteriores uma vez ao dia e retoma datas não cobertas após falhas. A extração de prazo e a sincronização com o Google Agenda processam filas a cada cinco segundos. Para executar sem Docker, use PostgreSQL 18, `make migrate-up`, `make run` e `make worker` em processos separados, com `DATABASE_URL` no ambiente.

As consultas de cadastro, sessões e CRUD de processos e prazos são geradas por [sqlc](https://sqlc.dev/) a partir de `db/queries` e `migrations`. Ao alterá-las, rode `make generate` com sqlc 1.31.1 e inclua `internal/store` atualizado. As transações de ingestão, sugestões, calendários e sincronização ainda usam SQL parametrizado nos serviços.

Crie o administrador inicial explicitamente, sem expor uma rota de promoção:

```sh
docker compose run --rm -e ADMIN_NAME='Administrador' -e ADMIN_EMAIL='admin@example.com' -e ADMIN_PASSWORD='senha-longa-unica' api /app/admin
```

Use uma senha real de pelo menos 12 caracteres. O comando atualiza a conta com esse e-mail caso ela já exista. Em produção, coloque API atrás de um proxy HTTPS, proteja segredos fora do repositório e configure SMTP apropriado. `SMTP_ADDR` e `SMTP_FROM` são necessários para recuperação de senha; o Mailpit incluído é apenas para desenvolvimento.

## Fluxo de uso

1. `POST /v1/auth/register` com nome, e-mail, senha, OAB, UF, `laborPractice` e **`llmConsent: true` obrigatório**. `POST /v1/auth/login` devolve um bearer token de sete dias. Contas antigas sem consentimento recebem `consentRequired: true` e só podem usar `/v1/me` até confirmar com `PATCH /v1/me`.
2. Cadastre o processo em `POST /v1/cases` com número CNJ de 20 dígitos e `tribunal` e `locality`. Para TJMS/TRT24, `locality` é a comarca; para TRF3, é a seção judiciária. Os dois últimos campos são necessários para calcular datas sugeridas.
3. Liste publicações com `GET /v1/publications?read=false`. A ingestão enfileira a análise automaticamente, inclusive para publicações ainda não lidas. Ao abrir uma publicação, chame `POST /v1/publications/{id}/open` para marcá-la como lida. O worker tenta as regras primeiro e chama o LLM como fallback quando configurado; consulte `GET /v1/publications/{id}` e `GET /v1/suggestions?publicationId={id}` para acompanhar o estado e a evidência. Sem evidência suficiente, a análise termina sem sugestão de prazo. Confirme ou corrija em `POST /v1/suggestions/{id}/confirm`, informando `title` e `dueOn`; uma falha definitiva pode ser reenfileirada com `POST /v1/publications/{id}/analyze`. Também é possível cadastrar um prazo manual vinculado à publicação. Prazos manuais ativos e sugestões confirmadas entram na fila do Google Agenda quando há um calendário conectado.
4. Opcionalmente conecte o Google em `GET /v1/google/connect`, abra `authorizationUrl`, consulte `/v1/google/calendars` e selecione um calendário editável em `PUT /v1/google/calendar`. Nenhum evento é enfileirado antes dessa escolha. O worker cria, atualiza e remove apenas eventos de prazos confirmados.

Todos os recursos de advogado exigem `Authorization: Bearer <token>` e são isolados por usuário. As rotas `/v1/admin/*` exigem papel de administrador.

## Calendários forenses e cálculo

O cálculo exige duas coberturas **aprovadas para todo o intervalo**: uma do tribunal e outra da comarca ou seção do processo. Sem ambas, a sugestão é salva **sem vencimento**. A versão aprovada precisa conter os dias sem expediente e de suspensão que incidam no escopo, além de fonte oficial e período de cobertura. Importações ficam como `DRAFT`; o administrador deve conferir o ato oficial e chamar `POST /v1/admin/calendars/{id}/approve` com `{"verifiedComplete":true}`. A API não atesta por si só a completude do ato importado.

`POST /v1/admin/calendars/import` recebe `tribunal`, `scopeType` (`TRIBUNAL`, `SECTION`, `COMARCA`), `scopeKey`, `coverageStart`, `coverageEnd`, `sourceTitle`, `sourceUrl` e `days[]` (`day`, `effect`, `title`, `authorityUrl`). Os efeitos são `CLOSED`, `DEADLINE_SUSPENDED`, `PARTIAL` e `INFO`. A [página oficial do TRT24](https://www.trt24.jus.br/calendario) pode ser convertida em rascunhos por tribunal e comarca com `go run ./cmd/calendar-prepare -year 2026`, ou dentro do Compose com `/app/calendar-prepare`. O comando guarda os JSONs e o hash da página em `private/calendar-drafts`. Envie cada JSON à rota de importação, confira as portarias, alterações e o alcance local, depois aprove. A página verificada em 27/09/2026 produziu 24 rascunhos para 2026, incluindo um dia de expediente parcial.

Para [TJMS](https://sistemas.tjms.jus.br/calculadora-prazos/) e [TRF3](https://www.trf3.jus.br/seju/calendario-dos-feriados), use importação estruturada com link para o ato oficial correspondente. Os PDFs, seções e alterações locais exigem conferência administrativa. A aplicação não ativa calendários a partir de raspagem automática dessas fontes.

O motor cobre prazo **expresso em dias úteis**, com ação identificada no texto e marco de disponibilização DJEN. Separa disponibilização, publicação no próximo dia útil e primeiro dia contado após a publicação, sem excluir esse primeiro dia novamente. Prazos implícitos, em horas, direcionados de forma incerta ou com calendário incompleto exigem análise manual. Alterações aprovadas em calendário ou publicação marcam prazos afetados para revisão, sem substituir a data confirmada.

Uma conferência manual na [calculadora oficial do TJMS](https://sistemas.tjms.jus.br/calculadora-prazos/), comarca Nova Andradina, usou publicação em 08/09/2026 e prazo de 3 dias úteis, sem incluir a data de publicação. A calculadora retornou 11/09/2026, igual ao teste do motor para disponibilização em 04/09/2026 (publicação após fim de semana e feriado de 07/09). Esse único confronto não valida outros feriados e regimes; os testes de virada de ano e suspensão usam calendários simulados.

## DJEN, LLM e pesquisa do TCC

A coleta padrão consulta `/comunicacao` na API oficial, com filtro por OAB/UF, tribunal, meio `D` e intervalo curto, percorrendo páginas de 100 itens. Publicações coletadas entram na fila de análise automaticamente. TJMS e TRF3 formam a cobertura principal; TRT24 é opcional para quem declarou atuação trabalhista. O onboarding consulta hoje, ontem e o último dia útil em uma busca por tribunal principal; TRT24 é coletado em segundo plano e não bloqueia a conclusão inicial. A coleta recorrente usa intervalos de até 14 dias, divididos se atingirem o limite de resultados do DJEN. Guarda a última data coberta por advogado e tribunal em `djen_lawyer_syncs.last_covered_on`; se o DJEN falhar, retoma do intervalo pendente e expõe a falha em `/v1/admin/sync`. Uma resposta vazia não é tratada como sucesso quando a paginação está incompleta. A ingestão confere a OAB/UF exata dos destinatários, preserva publicações sem prazo e compara o hash de cada comunicação para sinalizar revisão após alterações. Para auditoria ou recuperação por caderno, use `DJEN_COLLECTION_MODE=caderno` ou o replay de uma data: `docker compose run --rm worker /app/worker -tribunal TJMS -date 2026-09-25`. Em 27/09/2026, esse replay verificou o SHA-256 do ZIP do TJMS e gravou 10.964 comunicações.

As regras explicáveis são aplicadas primeiro. Se não encontrarem candidato, o fallback Cloudflare Workers AI roda para contas com consentimento obrigatório quando `CLOUDFLARE_ACCOUNT_ID` e `CLOUDFLARE_API_TOKEN` estiverem configurados. A API devolve `llmAssisted` em publicações, sugestões e prazos: em publicações, o campo fica verdadeiro quando o fallback foi chamado, mesmo se ele falhou; em sugestões e prazos, fica verdadeiro apenas quando a sugestão veio do LLM. O frontend pode usar essa informação para exibir o aviso ao advogado. O worker usa a interface `extract.Provider`, permitindo outra implementação; o modelo Cloudflare pode ser trocado em `CLOUDFLARE_MODEL`. CPF e e-mail são ocultados do texto enviado; a conta e o contrato do provedor precisam ser avaliados antes de usar publicações com outros dados pessoais. Falha ou quota esgotada não impede a publicação de aparecer.

Para pesquisar publicações da testadora autorizada, execute em ambiente com acesso ao DJEN:

```sh
go run ./cmd/research -oab 7242 -uf MS -pages 20 -out private/corpus.jsonl
```

O arquivo contém texto integral e fica fora do Git (`private/` é ignorado). Preencha `gold` em cada linha com os trechos exatos de prazos expressos em dias úteis e execute `go run ./cmd/evaluate -input private/corpus.jsonl` para obter TP, FP, FN, precisão e cobertura das regras. Veja [a avaliação exploratória e seus limites](doc/extraction-evaluation.md).

Em 27/09/2026, a [franquia publicada](https://developers.cloudflare.com/workers-ai/platform/pricing/) era de 10.000 Neurons por dia; o [documento de uso de dados](https://developers.cloudflare.com/workers-ai/platform/data-usage/) dizia que conteúdo do cliente não é usado para treinar modelos sem consentimento explícito. Confirme esses termos, a configuração da conta e a adequação do modelo antes de habilitar o fallback em produção.

## Integração Google

Configure um cliente OAuth Web no Google Cloud com callback `GOOGLE_REDIRECT_URI`, por exemplo `http://localhost:3000/v1/google/callback` em desenvolvimento. Defina `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` e uma chave AES de 32 bytes em base64 em `GOOGLE_TOKEN_KEY` (`openssl rand -base64 32`). Configure `WEB_APP_URL` com a origem fixa do frontend (`http://localhost:5173` no Vite ou `http://localhost:4173` no Compose); após o OAuth, o callback retorna para `/calendar` com o resultado da conexão. Essa origem não pode ser escolhida pela requisição. Com `WEB_APP_URL` definido, o e-mail de recuperação inclui um link para `/reset-password?token=...`; sem ele, as instruções legadas por token continuam disponíveis. O fluxo pede acesso offline e os escopos `calendar.events` e `calendar.calendarlist.readonly`. O refresh token é cifrado no banco. Desconectar interrompe novas sincronizações; eventos já criados permanecem no Google até serem excluídos ou geridos pelo usuário.

## Testes

```sh
go test ./...
go vet ./...
```

Com `DATABASE_URL` de um PostgreSQL migrado, os testes também exercitam isolamento entre advogados, ingestão de ZIP DJEN sintético, calendário e Google simulado. Esses testes criam e removem contas de teste. Não usam as credenciais reais de DJEN, Cloudflare ou Google.
