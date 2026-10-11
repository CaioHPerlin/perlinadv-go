# Deploy em `adv.perlin.com.br`

A interface React do repositório `CaioHPerlin/perlinadv-web` ocupa `/`. O Traefik encaminha `/v1` e `/health` diretamente para a API Go. API, worker, banco e interface não publicam portas no host. O Compose de desenvolvimento (`docker-compose.yml`) continua independente.

## Preparar a VPS

Na VPS Ubuntu 24.04, instale Docker Engine e o plugin Compose com `sudo bash scripts/install-docker-ubuntu.sh` deste repositório. Permita TCP 22 (ou a porta SSH escolhida), 80 e 443 na Security List/NSG da Oracle Cloud e no firewall local. Aponte o registro DNS `A` de `adv.perlin.com.br` para o IP de origem da VPS; o proxy da Cloudflare pode continuar ativo para HTTP(S), mas o SSH deve usar o IP direto. Se houver `AAAA`, ele também deve apontar para uma VPS que aceite 80/443 em IPv6. Use um usuário de deploy que possa executar Docker e que seja dono de `~/apps/perlinadv`.

```sh
docker network create traefik
mkdir -p ~/apps/perlinadv/{proxy,api/scripts,web/scripts,backups}
# Copie traefik.compose.yml para ~/apps/perlinadv/proxy/traefik.compose.yml.
printf 'ACME_EMAIL=seu-email@perlin.com.br\n' > ~/apps/perlinadv/proxy/.env.traefik
chmod 600 ~/apps/perlinadv/proxy/.env.traefik
cd ~/apps/perlinadv/proxy
docker compose --env-file .env.traefik -f traefik.compose.yml up -d
```

Não exponha o dashboard do Traefik. O resolver `letsencrypt` usa o desafio HTTP na porta 80 e armazena os certificados no volume `letsencrypt`.

Copie `.env.production.example` para `~/apps/perlinadv/api/.env.production` e substitua os valores. Use uma senha longa e segura para URL em `POSTGRES_PASSWORD`, repetida em `DATABASE_URL`. Configure SMTP real; Mailpit não integra o ambiente de produção. Execute `chmod 600` no arquivo. `WEB_APP_URL` deve ser `https://adv.perlin.com.br`; se usar Google Agenda, cadastre `https://adv.perlin.com.br/v1/google/callback` no OAuth e forneça `GOOGLE_TOKEN_KEY` gerada por `openssl rand -base64 32`. Se não usar Google ou Cloudflare, deixe as credenciais correspondentes vazias.

Os workflows usam o `GITHUB_TOKEN` temporário de cada repositório para baixar sua própria imagem privada do GHCR. O token é enviado ao processo de deploy pelo SSH e removido da configuração Docker ao final; não é necessário guardar um token de leitura na VPS.

## Configurar GitHub Actions

Nos dois repositórios, crie o ambiente `production` e configure estes secrets nele:

| Secret | Valor |
| --- | --- |
| `DEPLOY_HOST` | Host/IP público da VPS |
| `DEPLOY_PORT` | Porta SSH, normalmente `22` |
| `DEPLOY_USER` | Usuário de deploy na VPS |
| `DEPLOY_SSH_KEY` | Chave privada dedicada aos workflows |
| `DEPLOY_KNOWN_HOSTS` | Linha de host key da VPS, validada fora do GitHub |

Adicione a chave pública correspondente a `~/.ssh/authorized_keys` do usuário de deploy. Para `DEPLOY_KNOWN_HOSTS`, obtenha a chave com `ssh-keyscan -p PORTA HOST` e confira a impressão digital contra a chave da VPS antes de salvá-la. Configure os secrets e a variável de ambiente `DEPLOY_ENABLED=true` **antes** de ativar o deploy. Enquanto a variável não existir, o workflow testa e publica a imagem, mas não acessa a VPS. Os workflows publicam imagens privadas `ghcr.io/caiohperlin/perlinadv:<sha>` e `ghcr.io/caiohperlin/perlinadv-web:<sha>`; o `GITHUB_TOKEN` de cada repositório precisa ter acesso ao próprio pacote.

O primeiro deploy da API pode ocorrer antes da interface. O script cria `.env.release` com a tag, inicia o banco, aplica migrações e inicia API e worker. O workflow web publica e inicia a interface no mesmo domínio. Configure DNS antes de verificar HTTPS. A ordem dos deploys subsequentes é independente desde que mudanças de API e banco sejam compatíveis com a versão anterior da interface.

Para criar a primeira conta administradora depois da migração, defina `ADMIN_NAME`, `ADMIN_EMAIL` e uma `ADMIN_PASSWORD` de pelo menos 12 caracteres e execute o comando abaixo na VPS. Guarde a senha em local protegido; o comando atualiza a senha se o e-mail já existir.

```sh
cd ~/apps/perlinadv/api
ADMIN_NAME='Seu nome' ADMIN_EMAIL='seu-email@example.com' ADMIN_PASSWORD='senha-longa-gerada' \
  docker compose --env-file .env.production --env-file .env.release -f compose.production.yml \
  run --rm --no-deps -e ADMIN_NAME -e ADMIN_EMAIL -e ADMIN_PASSWORD api /app/admin
```

Depois de ativar `DEPLOY_ENABLED`, execute manualmente o workflow **CI and deploy** na `main` de cada repositório para implantar o commit atual. Pushes seguintes fazem deploy automaticamente.

## Conferir e recuperar

```sh
curl -fsS https://adv.perlin.com.br/health
curl -fsSI https://adv.perlin.com.br/
docker compose --env-file ~/apps/perlinadv/api/.env.production --env-file ~/apps/perlinadv/api/.env.release -f ~/apps/perlinadv/api/compose.production.yml ps
docker compose --env-file ~/apps/perlinadv/web/.env.release -f ~/apps/perlinadv/web/compose.production.yml ps
```

Para voltar a uma imagem anterior **já baixada na VPS**, rode `bash ~/apps/perlinadv/api/scripts/deploy-production.sh SHA_ANTERIOR` ou o script equivalente em `web/scripts`. Se a imagem não estiver em cache, passe um token com leitura do pacote privado pelo stdin com `GHCR_LOGIN=1 GHCR_USER=CaioHPerlin`; o token é descartado ao final. O script de API roda as migrações novamente; Goose não reverte migrações automaticamente. Migrações futuras precisam ser compatíveis com a versão anterior até terminar a janela de rollback. Um erro de health check durante o deploy tenta restaurar a tag anterior, mas não desfaz alterações no banco.

Antes de cada migração de produção, mantenha backup testado do banco. Exemplo de backup manual, executado na VPS pelo usuário de deploy:

```sh
cd ~/apps/perlinadv/api
umask 077
docker compose --env-file .env.production --env-file .env.release -f compose.production.yml exec -T db pg_dump -U perlinadv -Fc perlinadv > ~/apps/perlinadv/backups/perlinadv-$(date -u +%Y%m%dT%H%M%SZ).dump
```

Agende esse comando diariamente, mantenha uma cópia fora da VPS e teste uma restauração em banco isolado. Para restaurar, pare API e worker, escolha um backup e execute `pg_restore` em um banco vazio; só depois retome os serviços. Restaurar substitui dados criados após o backup, portanto não é o procedimento normal de rollback de imagem.
