# Avaliação das regras de extração

Em 27/09/2026, a consulta pública DJEN da OAB/MS 7242 forneceu 315 publicações nos 365 dias anteriores. O texto integral foi mantido apenas em `private/`, fora do Git. Em leitura exploratória, quatro publicações continham prazo processual expresso em **dias úteis**; três outras menções a dias úteis descreviam duração de sessão ou preparação para perícia. A regra `rules-v2` foi ajustada com base nessa amostra e, nela, obteve TP=4, FP=0, FN=0. Esse resultado é **exploratório** e não estima desempenho geral.

Um período histórico separado, anterior a 27/09/2025, reuniu 254 publicações da mesma OAB. Não havia prazo expresso em dias úteis nesse conjunto; a regra não produziu falsos positivos. Precisão e cobertura nesse período são indefinidas porque não houve candidatos nem positivos rotulados. A avaliação com positivos reservados continua necessária antes de reivindicar precisão fora da amostra exploratória.

Os rótulos consideram somente o trecho literal que estabelece prazo processual em dias úteis. Prazos expressos sem a unidade “úteis”, prazos legais implícitos, duração de sessões e datas condicionadas a outro marco não contam como positivos para **cálculo automático**. Esses casos continuam visíveis na publicação e podem ser lançados manualmente. A revisão humana é obrigatória também para os quatro positivos.

Para repetir a coleta autorizada sem publicar dados pessoais, use `cmd/research` com `-oab`, `-uf`, `-days`, `-pages` e `-out private/...`; rotule `gold` localmente e execute `cmd/evaluate`. O avaliador reporta `n/a` quando a métrica não tem denominador. Nenhum texto do corpus foi incorporado ao repositório.
