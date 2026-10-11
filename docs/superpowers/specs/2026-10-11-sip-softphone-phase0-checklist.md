# Softphone SIP — Fase 0: roteiro do spike

Data: 2026-10-11 · Spec: `2026-10-11-sip-softphone-design.md` (rev 3)

Roteiro **genérico**. O nome do provedor bloqueia a *execução*, não a
preparação: ao escolher o provedor, preencher a seção "Adaptação ao provedor" e
ajustar os procedimentos que dependem dele.

## Regras do spike

- **Descartável.** Uma página de teste local com JsSIP. Nada do spike vai para o
  produto.
- **Quem executa:** o dono do ramal de teste. Credenciais **nunca** são
  coladas em chat, commitadas ou escritas neste documento.
- **Evidência sanitizada:** troque senha, `Authorization`/`Proxy-Authorization`,
  tokens, IPs internos e números reais por `<redigido>` antes de anexar logs.
- **Conclusão por teste:** exatamente uma de
  - **validado** — funcionou com evidência;
  - **não suportado** — o provedor/navegador não oferece, com evidência;
  - **inconclusivo** — não foi possível decidir; descrever o que faltou.
  "Parece funcionar" é inconclusivo.

## 0. Pré-requisitos

Preencher antes de qualquer teste. **Não registrar senhas.**

| Item | Valor |
|---|---|
| Provedor / plano | |
| Ramal de teste (usuário, sem senha) | |
| URL WSS | |
| Domínio / realm SIP | |
| Suporta SRTP + ICE (WebRTC) no ramal? | |
| Navegador e versão | |
| Sistema operacional | |
| Rede do teste (casa, escritório, 4G) e NAT | |
| STUN/TURN usados (hosts, **sem** credenciais) | |
| Fonte das credenciais TURN (provedor ou `GET /api/calls/ice-servers`) | |
| Número externo de teste A / B (mascarado) | |
| Segundo ramal para chamada interna | |
| Acesso a CDR/painel do provedor? | |

Critério de entrada: ramal registrável pelo console do provedor e dois números
de teste disponíveis. Sem isso, parar.

## 1. Testes de capacidade

Para cada teste: **Procedimento → Esperado → Evidência → Conclusão**.

### T1. Registro WSS

- **Procedimento:** registrar o ramal com JsSIP na URL WSS; manter 10 min.
- **Esperado:** `registered`; renovação do registro sem queda; desregistro limpo.
- **Evidência:** log do UA com eventos `registered`/`unregistered` e o tempo.
- **Variantes:** senha errada (esperado: `registrationFailed` 401/403 tratável).
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T2. Chamada de saída para número externo

- **Procedimento:** discar para o número de teste A; atender; ficar 30s; desligar.
- **Esperado:** sinalização de toque, atendimento, encerramento; estados na ordem
  `initiating → ringing → answered → completed`.
- **Evidência:** sequência de eventos com horários; Call-ID da perna.
- **Registrar:** o provedor emite *early media*/toque ou o toque é local?
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T3. Chamada recebida

- **Procedimento:** ligar do número A para o número do ramal; atender; desligar.
  Repetir **recusando** e **deixando tocar** até cair.
- **Esperado:** evento de convite recebido; atender, recusar (486/603) e perda
  tratáveis; o Call-ID recebido é registrado.
- **Evidência:** eventos e Call-ID de cada cenário.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T4. Áudio bidirecional e codecs

- **Procedimento:** em chamada atendida, falar nos dois lados e conferir áudio.
  Capturar o SDP negociado (`getStats` ou log do UA).
- **Esperado:** áudio nos dois sentidos; codec negociado (Opus, PCMU ou PCMA);
  sem mudo unilateral; latência perceptível aceitável.
- **Evidência:** SDP final **sanitizado**, codec escolhido, `getStats`
  (packetsReceived/Sent > 0 nos dois sentidos), observação humana.
- **Registrar:** transcodificação no provedor? Qualidade com PCMU vs Opus.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T5. DTMF

- **Procedimento:** chamar uma URA ou número que ecoe tons; enviar `1`, `#`,
  `*`, sequência longa.
- **Esperado:** tons recebidos corretamente; identificar o método (RFC 2833/
  telephone-event ou SIP INFO).
- **Evidência:** método usado, SDP com `telephone-event`, resultado na URA.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T6. Espera e retomada

- **Procedimento:** em chamada, colocar em espera (re-INVITE `sendonly`/
  `inactive`) e retomar. Do outro lado, ouvir música/silêncio e confirmar
  retomada do áudio.
- **Esperado:** espera e retomada sem derrubar a chamada; o outro lado percebe.
- **Evidência:** eventos `hold`/`unhold`, SDP de cada re-INVITE, confirmação do
  lado remoto.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T7. Transferência cega

- **Procedimento:** com chamada ativa A↔ramal, transferir para o número/ramal C
  por REFER (sem consulta).
- **Esperado:** a chamada passa a A↔C; o ramal sai da chamada; eventos de
  progresso (NOTIFY) recebidos.
- **Evidência:** `REFER` aceito (202), NOTIFY `sipfrag`, resultado em C.
- **Registrar:** destinos permitidos (interno, externo); o provedor restringe?
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T8. Transferência assistida

- **Procedimento:** com A em espera, ligar para C (consulta), conversar, e
  completar a transferência (REFER com `Replaces`).
- **Esperado:** A↔C conectados; ramal fora; consulta cancelável sem derrubar A.
- **Evidência:** Replaces aceito, estados das duas pernas, resultado.
- **Nota:** REFER cego validado **não** comprova este fluxo.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T9. ICE e TURN

- **Procedimento:** repetir T2/T4 em três redes: sem NAT restritivo, NAT
  doméstico e rede que bloqueia UDP (usar TURN/TCP/TLS se disponível).
- **Esperado:** par ICE `connected`; mídia fluindo; fallback via TURN quando
  UDP é bloqueado.
- **Evidência:** `iceConnectionState`, par de candidatos selecionado (tipo
  host/srflx/relay), por rede.
- **Registrar:** as credenciais TURN do `GET /api/calls/ice-servers` servem ao
  provedor? Se não, de onde vêm?
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T10. Suspensão da aba e reconexão

- **Procedimento:**
  a. aba em segundo plano 10 min durante chamada; conferir áudio e timers;
  b. cortar a rede 20s e restaurar, com chamada em curso e com ramal ocioso;
  c. fechar a aba durante a chamada e observar o que o provedor faz.
- **Esperado:** (a) mídia continua e quantos segundos o timer atrasa; (b)
  reconexão do WSS e re-registro, chamada sobrevive ou cai de forma
  identificável; (c) o provedor encerra a chamada ou mantém.
- **Evidência:** linha do tempo de eventos; quanto atrasou o heartbeat de 30s;
  se a mídia continuou.
- **Alimenta:** valores de heartbeat/reaper (30s / 3 min) da spec.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### T11. Duas abas / dois registros

- **Procedimento:** registrar o mesmo ramal em duas abas e em dois navegadores.
- **Esperado:** descobrir se o provedor aceita múltiplos registros, derruba o
  anterior ou toca nos dois.
- **Evidência:** comportamento de registro e de chamada recebida.
- **Alimenta:** a exclusão de registro por `navigator.locks` e o limite
  "entre navegadores" da spec.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

## 2. Gravação e CDR

Esta seção decide a fase 3. Fazer com 3 chamadas distintas: saída atendida,
entrada atendida e uma **transferida**.

### G1. Identificadores por perna

Para cada chamada, preencher:

| Chamada | Call-ID SIP no navegador | ID da chamada no CDR | ID da gravação | Pernas (A↔ramal, ramal↔C) | IDs iguais entre pernas? |
|---|---|---|---|---|---|
| Saída | | | | | |
| Entrada | | | | | |
| Transferida | | | | | |

- **Esperado:** descobrir **qual identificador** correlaciona navegador, CDR e
  gravação, e se ele muda em transferência/encaminhamento.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### G2. Correlação com a chamada do Whatc

- **Procedimento:** tentar, **apenas com dados disponíveis ao Whatc**
  (`sip_call_id`, horários, números, ramal), localizar a gravação certa no
  provedor para as 3 chamadas, sem olhar o painel.
- **Esperado:** correlação **determinística** (identificador exato), não por
  proximidade de horário. Registrar se há ambiguidade (duas chamadas
  simultâneas do mesmo ramal ou número).
- **Evidência:** a regra de correlação usada e o resultado nas 3 chamadas.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### G3. Acesso à gravação

- **Procedimento:** obter o arquivo da gravação **programaticamente** (API do
  provedor) para uma das chamadas.
- **Registrar:** autenticação da API, formato e codec do arquivo, estéreo ou
  mono (pernas separadas?), atraso até ficar disponível, retenção, URL
  temporária ou permanente, custo por gravação/armazenamento.
- **Esperado:** download via API com credencial de servidor (nunca do navegador).
- **Evidência:** chamada de API sanitizada e metadados do arquivo.
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

### G4. CDR

- **Registrar:** campos disponíveis (início, atendimento, fim, duração,
  disposição, número, ramal, ID), atraso, API ou webhook, e como se reconcilia
  com o histórico operacional do Whatc (fora de escopo da fase 1, mas o dado
  precisa existir).
- **Conclusão:** ☐ validado ☐ não suportado ☐ inconclusivo

## 3. Conclusão e recomendação

### 3.1 Quadro de resultados

| Teste | Conclusão | Evidência (link/arquivo) | Observação |
|---|---|---|---|
| T1 Registro | | | |
| T2 Saída | | | |
| T3 Entrada | | | |
| T4 Áudio/codecs | | | |
| T5 DTMF | | | |
| T6 Espera | | | |
| T7 Transf. cega | | | |
| T8 Transf. assistida | | | |
| T9 ICE/TURN | | | |
| T10 Suspensão/reconexão | | | |
| T11 Várias abas | | | |
| G1 Identificadores | | | |
| G2 Correlação | | | |
| G3 Acesso | | | |
| G4 CDR | | | |

### 3.2 Critérios de avanço (por fase)

Os critérios são **independentes**: uma capacidade não suportada bloqueia só a
fase que depende dela.

| Fase | Bloqueia se | Não bloqueia |
|---|---|---|
| **1 — Núcleo** | T1, T2, T3 ou T4 **não suportado** ou **inconclusivo**; T9 sem caminho de mídia em NAT doméstico | T5–T8, T11 e toda a seção G |
| **2 — Controles** | T6 ou T5 não suportado bloqueia o item correspondente; T7 não suportado bloqueia só a transferência | **T8 (assistida) não suportada não bloqueia** o núcleo nem a fase 2: entrega só cega |
| **3 — Gravação** | **G1 ou G2 sem correlação determinística comprovada**, ou G3 sem acesso programático | Falhas em T5–T8; a fase 3 não depende delas |

Regras adicionais:

- Gravação "existe no painel" **não** é critério: o que vale é o Whatc
  localizar e acessar a gravação certa (G2 + G3).
- T10 e T11 não bloqueiam, mas **ajustam parâmetros** da spec (heartbeat/
  reaper, política de abas) e podem exigir revisar a spec antes do plano.
- Resultado **inconclusivo** em um critério bloqueante conta como bloqueio até
  ser refeito.

### 3.3 Bloqueadores e limitações

Listar, por teste, o que impede ou limita, com a evidência:

- Bloqueadores:
- Limitações aceitas:
- Itens a refazer (inconclusivos):

### 3.4 Recomendação por fase

| Fase | Recomendação | Justificativa |
|---|---|---|
| 1 | ☐ avançar ☐ avançar com ressalvas ☐ não avançar | |
| 2 | ☐ avançar ☐ avançar parcial (sem assistida) ☐ não avançar | |
| 3 | ☐ avançar ☐ adiar ☐ revisar arquitetura (considerar B2BUA) | |

### 3.5 Ajustes sugeridos à spec

- Heartbeat/reaper (valores medidos em T10):
- Política de múltiplos registros (T11):
- Codec e DTMF a priorizar (T4/T5):
- Identificador de correlação a usar em `sip_call_id`/campo novo (G1):

## 4. Adaptação ao provedor (preencher ao escolher)

- Nome do provedor e plano:
- Documentação WSS/WebRTC e limites do ramal:
- Peculiaridades de transferência/espera:
- API de CDR e gravação (endpoints, autenticação):
- Ajustes nos testes acima:

## 5. Histórico de execução

| Data | Executor | Provedor | Navegador | Resultado geral |
|---|---|---|---|---|
| | | | | |
