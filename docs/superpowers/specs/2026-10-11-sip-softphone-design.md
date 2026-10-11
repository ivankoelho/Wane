# Softphone SIP no navegador — design

Data: 2026-10-11 · Status: revisão 3 — arquitetura aprovada; fase 1 condicionada
ao relatório da fase 0. Compatibilidade de migrations, permissões e integração
com o código existente ainda precisa ser verificada no repositório (no plano).

## Objetivo

Atendentes ligam e recebem chamadas telefônicas comuns (PSTN) dentro do Whatc,
pelo navegador, via SIP sobre WebSocket (WSS) direto com o provedor. Cada
atendente tem seu próprio ramal. As chamadas aparecem no mesmo histórico
(`call_logs`) das chamadas de WhatsApp.

## Decisões já tomadas

| Tema | Decisão |
|---|---|
| Arquitetura | Navegador registra e carrega a mídia direto com o provedor (JsSIP). O servidor do Whatc não vê o áudio. |
| Provedor | Brasileiro, com SIP sobre WSS/WebRTC. **Nome ainda não definido (item em aberto 1).** |
| Credenciais | Um ramal por atendente. |
| Escopo geral | Ligar, receber, histórico, espera, transferência, DTMF, gravação. |
| Entrega | Fases 0 a 3, cada uma com plano e PR próprios a partir da fase 1. |

## Fora de escopo

- Servidor intermediando a mídia (B2BUA). Reavaliar só se o provedor não permitir
  gravação/CDR por API ou se a senha SIP no navegador for inaceitável.
- Receber chamadas com a aba fechada.
- Reconciliação com CDR do provedor para faturamento/auditoria.
- Exclusividade de chamada **entre navegadores ou dispositivos diferentes**
  (limite declarado: os locks valem dentro de um navegador).

## Fases

0. **Spike (descartável).** Página de teste com JsSIP contra um ramal de teste,
   rodada pelo dono do ramal. Cada capacidade é concluída como **validada**,
   **não suportada** ou **inconclusiva**, com a evidência:
   registro WSS; ligar e receber; codecs; DTMF; transferência cega e assistida
   (REFER sozinho não prova ambas); espera; ICE/TURN; suspensão de aba e
   reconexão; qual identificador correlaciona **CDR e gravação** com a chamada
   (o Call-ID SIP pode mudar entre pernas); como o Whatc **localiza e acessa** a
   gravação certa, não só "o provedor grava". Saída: um relatório, não código do
   produto.
1. **Núcleo.** Ramais, ligar, receber com toque, mudo, desligar, histórico,
   exclusão mútua, várias abas. Condicionada ao relatório da fase 0.
2. **Controles.** Espera (re-INVITE), DTMF e **transferência cega**. A
   transferência assistida entra só se a fase 0 a validar.
3. **Gravação.** Gravação feita pelo provedor, correlacionada pelo identificador
   que a fase 0 confirmar. Gravação local no navegador **não** é solução (aba
   fechada perde o áudio).

## Backend (fase 1)

### Ramais

Tabela `sip_extensions`: `organization_id`, `user_id` (único por organização),
`wss_url`, `domain`, `username`, `auth_username` (opcional), `password`,
`display_name`, `enabled`.

- Senha criptografada em repouso, reaproveitando o mecanismo de criptografia de
  chaves de IA existente (`internal/database/ai_key_encryption.go`).
- API administrativa é **write-only**: nunca devolve a senha cadastrada.
- `GET /api/sip/config` devolve apenas o ramal do usuário autenticado, com a
  senha, porque o navegador precisa dela. Responde `Cache-Control: no-store`, só
  por HTTPS. A senha não é logada, não vai a telemetria e não é gravada em
  storage persistente do frontend.
- **Limite declarado:** como a credencial está no navegador, bloquear o botão
  de discagem não impede chamadas externas feitas por outro cliente SIP com a
  mesma credencial. Restrição efetiva de destinos (países, prefixos, limite de
  gasto) deve ser configurada **no provedor/ramal**.

### Histórico (`call_logs`)

Colunas novas:

- `channel` (`whatsapp` padrão, `sip`).
- `sip_call_id` — o identificador **SIP** da chamada. Indexado, **não único**:
  encaminhamento, transferência ou vários ramais podem gerar mais de uma linha
  com o mesmo Call-ID, e o provedor pode alterá-lo entre pernas.
- `client_call_ref` — UUID gerado pelo navegador. É a chave de **idempotência**,
  com índice único `(organization_id, agent_id, client_call_ref)`. Repetir a
  criação devolve o registro do **mesmo agente**, nunca o de outro. Momento da
  geração: **saída** — na criação da tentativa; **entrada** — assim que a sessão
  recebida é detectada, **antes** de atender, recusar ou rejeitar por ocupação,
  para que chamadas recusadas e perdidas também tenham referência. Retries
  reutilizam a mesma referência.
- `outcome_reason` — motivo estruturado (por exemplo `client_timeout`), usado
  pela interface e pelos indicadores sem interpretar `error_message`.
- `duration_known` (boolean, padrão `true`) — distingue **duração zero
  conhecida** (cancelamento legítimo antes de atender) de **duração
  desconhecida** (`completed` sem `answered`, ou recuperação parcial). Não se usa
  `answered_at = null` para isso. A interface mostra "—" quando
  `duration_known = false`.
- `corrections` (jsonb) — registro das correções por episódio de timeout
  (quando, evento recebido, estado anterior e posterior).

Regra por canal: `whatsapp_account` é obrigatório para `whatsapp` e vazio para
`sip`. O plano inclui auditar consultas, serializers e filtros que assumem a
conta preenchida.

O histórico SIP é **operacional, não auditável**: o navegador pode atrasar,
perder ou manipular eventos.

### Endpoints e permissões (matriz por operação)

| Operação | Endpoint | Permissão |
|---|---|---|
| Ler a própria configuração | `GET /api/sip/config` | `sip_calls:read` |
| Criar registro de chamada recebida | `POST /api/sip/calls` com `direction=incoming` | `sip_calls:read` |
| Criar registro de chamada de saída (discar para externo) | `POST /api/sip/calls` com `direction=outgoing` | `sip_calls:write` |
| Atualizar desfecho (atendida, recusada, perdida, encerrada) | `PATCH /api/sip/calls/{id}` | pela direção **persistida** do registro |
| Heartbeat | `POST /api/sip/calls/{id}/heartbeat` | pela direção **persistida** do registro |
| Gerir ramais | `/api/sip/extensions` | `sip_extensions` |

A permissão de discar externamente não impede o registro de uma chamada
recebida. Toda autorização confere **organização e agente**; registro de outro
agente ou organização responde 404.

### Máquina de estados

Estado inicial persistido: saída `initiating`, entrada `ringing`.

| De | Evento | Para | Observação |
|---|---|---|---|
| `initiating` | provedor sinaliza toque | `ringing` | |
| `initiating` | atendida sem evento de toque | `answered` | grava `answered_at` |
| `ringing` | atendida | `answered` | grava `answered_at` |
| `answered` | encerrada | `completed` | `duration = ended_at − answered_at` |
| `initiating`/`ringing` | agente cancela (saída) | `completed` | `answered_at` nulo, `duration = 0`, `duration_known = true`, `disconnected_by=agent` (igual ao WhatsApp) |
| `initiating`/`ringing` | destino recusa | `rejected` | |
| `ringing` | agente recusa (entrada) | `rejected` | |
| `ringing` | ninguém atende / originador cancela (entrada) | `missed` | |
| qualquer não terminal | erro de rede/SIP | `failed` | |
| `initiating`/`ringing` | `completed` recebido sem `answered` | `completed` | evento intermediário perdido: aceita; `answered_at` nulo, `duration = 0`, **`duration_known = false`** |
| `failed` com `client_timeout` | evento tardio com desfecho real | `answered`/`completed`/etc. | **correção por episódio de timeout** (ver "Recuperação após `client_timeout`") |

Estados terminais: `completed`, `rejected`, `missed` e `failed` (exceto
`failed` com `outcome_reason=client_timeout`, corrigível). Evento tardio sobre
outro terminal é descartado e logado.

**Autorização pela direção persistida:** no `PATCH` e no heartbeat, a permissão
é determinada pela `direction` **já gravada** no registro, nunca pela recebida
no request. A direção é **imutável** após a criação (o `PATCH` a ignora e um
valor divergente responde 422).

**Concorrência:** cada atualização roda em transação com `SELECT … FOR UPDATE`
na linha e aplica a tabela acima sobre o estado lido; requests concorrentes são
serializados e o perdedor reavalia. "Mesmo estado" é no-op.

### Chamadas órfãs

O navegador envia heartbeat a cada 30s (`last_seen_at`). Sem sinal por 3 min
(configuração inicial, validada nos testes de suspensão e reconexão), um job
marca a chamada `failed` com `outcome_reason=client_timeout` e
`disconnected_by=system`. A mídia pode continuar com timers limitados na aba,
então esse é um desfecho **desconhecido**, não falha confirmada:

- a interface mostra "desfecho desconhecido", sem depender de `error_message`;
- indicadores e relatórios **não** contam `client_timeout` como falha;
- um evento tardio corrige a linha conforme a tabela acima.

**Reaper:** usa a mesma proteção transacional das atualizações. Seleciona
candidatas, bloqueia a linha (`FOR UPDATE`) e **revalida `last_seen_at` depois
do lock** antes de marcar; um heartbeat que chegou no meio impede a marcação.

### Recuperação após `client_timeout`

A correção vale **por episódio de timeout**, não "uma única vez" na vida da
chamada:

- Ao receber evento tardio ou heartbeat de uma chamada em `client_timeout`, a
  linha volta ao estado real informado (`answered`, `completed`, ...).
- A recuperação **limpa os campos de encerramento provisório** (`ended_at`,
  `outcome_reason`, `error_message`, `disconnected_by=system`), **atualiza
  `last_seen_at`** e permite o encerramento normal posterior.
- Se a chamada voltar a ficar órfã depois, um **novo** episódio de timeout
  começa e pode ser corrigido de novo.
- Cada episódio e cada correção ficam em `corrections`, mantendo a trilha das
  alterações.

## Frontend (fase 1)

- `services/sip.ts`: encapsula o JsSIP (registro, ligar, atender, mudo,
  desligar) e expõe eventos simples. Nenhum detalhe SIP vaza para componentes.
- `stores/sip.ts`: estado do softphone, separado do `calling.ts`.
- **Estados separados.** Registro: `conectando`, `registrado`, `recusado`,
  `reconectando`, `offline`. Chamada: `ociosa`, `chamando`, `tocando`,
  `em andamento`, `encerrando`.

### Exclusão mútua (dois locks)

1. **Registro SIP** — `navigator.locks` com `sip-register-<userId>`: só a aba
   que o detém registra o ramal e recebe chamadas. As demais mostram "ramal
   ativo em outra aba".
2. **Chamada do atendente** — lock compartilhado **entre abas e entre canais**
   (SIP e WhatsApp), mantido durante **toda** a chamada. Usa
   `navigator.locks` mais um canal (`BroadcastChannel`) para estado visível.
   Tem **proprietário/token**: um evento atrasado de uma chamada não libera a
   reserva de outra. A aquisição é assíncrona (`navigator.locks.request`), por
   isso: **nenhuma operação de chamada — discagem, atendimento ou acesso à
   mídia — começa antes da confirmação de aquisição do lock.** A aquisição usa
   `ifAvailable: true` (sem espera), para que um clique bloqueado não fique
   enfileirado e disque depois. O callback do lock permanece pendente durante
   toda a chamada e só é resolvido ao encerrá-la.

Regras:

- Chamada recebida enquanto o atendente está ocupado: SIP é recusado com 486
  (busy) e registrado como `missed`; transferência de WhatsApp segue o fluxo de
  fila existente, sem tocar no atendente.
- Logout e troca de organização liberam os dois locks e encerram o registro.
- `navigator.locks` ausente (navegador antigo): o softphone fica **desativado**
  com aviso, em vez de rodar sem exclusão.
- Limite declarado: sem exclusividade entre navegadores ou dispositivos
  diferentes do mesmo atendente.

### Demais pontos

- **Toque.** Reaproveita `startRingTone` / `stopRingTone` de `calling.ts`
  (passam a ser exportados).
- **ICE.** Reaproveita `GET /api/calls/ice-servers`; a fase 0 valida que as
  credenciais TURN servem ao provedor.
- **Erros visíveis:** ramal não registrado, registro recusado, sem microfone,
  autoplay bloqueado, falha de ICE.
- **UI:** discador compacto e banner de chamada recebida (atender/recusar). No
  contato, o botão de ligar oferece WhatsApp ou telefone.
- **WhatsApp:** o comportamento é preservado, **exceto pela exclusão mútua
  deliberada**: com uma chamada SIP em curso, iniciar ou atender WhatsApp passa
  a ser bloqueado, e vice-versa.

## Testes obrigatórios

- Isolamento entre organizações e entre agentes (ler/atualizar log alheio → 404).
- Idempotência: criar 2x com o mesmo `client_call_ref` devolve o mesmo registro;
  nunca o de outro agente; duas pernas com o mesmo `sip_call_id` não colidem.
- Máquina de estados: cada linha da tabela (incluindo `initiating → answered`),
  eventos fora de ordem, duplicados, concorrentes (dois requests diferentes na
  mesma chamada), `duration_known` em cancelamento legítimo vs `completed` sem
  `answered`.
- Recuperação de `client_timeout`: limpa campos provisórios, atualiza
  `last_seen_at`, permite encerramento normal, segundo episódio de timeout
  corrigível, `corrections` registra cada um; heartbeat concorrente ao reaper
  impede a marcação (revalidação após o lock).
- Autorização pela direção persistida: `PATCH`/heartbeat com `direction`
  divergente no request não escala permissão; direção imutável.
- `client_call_ref` gerado antes de atender/recusar: chamada recusada por
  ocupação já tem referência, e retries não duplicam.
- Lock: clique com lock ocupado não enfileira nem disca depois (`ifAvailable`).
- Chamadas entre dois atendentes da mesma organização.
- Chamada SIP em uma aba e WhatsApp em outra (deve bloquear); token impede que
  evento atrasado libere reserva alheia.
- Chamada recebida com atendente ocupado (486 e `missed`).
- Duas abas: só uma registra; a segunda assume ao fechar a primeira; logout e
  troca de organização liberam os locks.
- Perda de rede (reconexão do registro; chamada sem heartbeat) e suspensão da aba.
- Permissões por operação: receber sem `write`; discar exige `write`.
- Senha nunca na resposta administrativa nem em log; `no-store` presente.
- Regressão das chamadas de WhatsApp: listagem de `call_logs`, saída e entrada.

## Riscos

- **Senha SIP no navegador** é inevitável no modelo A. Mitigação: ramal por
  atendente, rotação pelo admin e restrição de destinos no provedor.
- **Dependência do provedor** para gravação e CDR. Mitigação: fase 0 antes de
  qualquer código de produto; fase 3 só com correlação comprovada.
- **Histórico depende do navegador.** Aceito na fase 1, declarado como
  operacional.
- **Timers de aba suspensa** podem gerar desfecho desconhecido. Mitigação:
  `client_timeout` corrigível e fora dos indicadores.

## Itens em aberto

1. Nome do provedor brasileiro. **Bloqueia a execução do spike** (fase 0).
2. Valores finais de heartbeat/reaper (iniciais: 30s e 3 min), a validar nos
   testes de suspensão e reconexão.
3. Transferência assistida na fase 2: depende do relatório da fase 0.
