# Softphone SIP no navegador — design

Data: 2026-10-11 · Status: aguardando revisão

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
| Entrega | Fases 0 a 3, cada uma com spec/plano/PR próprios a partir da fase 1. |

## Fora de escopo

- Servidor intermediando a mídia (B2BUA). Reavaliar só se o provedor não permitir
  gravação/CDR por API ou se a senha SIP no navegador for inaceitável.
- Receber chamadas com a aba fechada.
- Reconciliação com CDR do provedor para faturamento/auditoria.
- Alterar o fluxo das chamadas de WhatsApp.

## Fases

0. **Spike (descartável).** Página de teste com JsSIP contra um ramal de teste,
   rodada pelo dono do ramal. Valida: registro WSS, ligar e receber, codecs,
   DTMF, REFER, ICE/TURN, e como obter gravação e CDR do provedor. Saída: um
   relatório, não código do produto.
1. **Núcleo.** Ramais, ligar, receber com toque, mudo, desligar, histórico,
   exclusão mútua, várias abas.
2. **Controles.** Espera (re-INVITE), transferência (REFER) e DTMF.
3. **Gravação.** Gravação feita pelo provedor, correlacionada por `sip_call_id`.
   Só entra depois de a fase 0 confirmar a capacidade. Gravação local no
   navegador **não** é solução (aba fechada perde o áudio).

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

### Histórico (`call_logs`)

- Novas colunas: `channel` (`whatsapp` padrão, `sip`) e `sip_call_id`.
- Regra por canal: `whatsapp_account` é obrigatório para `whatsapp` e vazio
  para `sip`. O plano inclui auditar consultas, serializers e filtros que
  assumem a conta preenchida.
- Índice único `(organization_id, sip_call_id)` para idempotência.
- `POST /api/sip/calls` cria o log (direção, número, `sip_call_id`) e vincula
  `contact_id` pelo telefone com a normalização existente. Repetir a chamada
  devolve o mesmo registro.
- `PATCH /api/sip/calls/{id}` aceita `answered`, `completed`, `rejected`,
  `missed`, `failed`. Mesmo estado repetido é no-op. Transições inválidas
  (por exemplo, reabrir `completed`) são recusadas.
- Toda autorização confere **organização e agente**. Registro de outro agente
  ou organização responde 404.
- Horários carimbados pelo servidor. `duration` = `answered_at → ended_at`
  (conversa). Tempo de toque = `started_at → answered_at`.
- O histórico SIP é **operacional, não auditável**: o navegador pode atrasar,
  perder ou manipular eventos.

### Chamadas órfãs

O navegador envia heartbeat a cada ~30s (`last_seen_at`), com tolerância para
aba suspensa e reconexão. Sem sinal por um prazo configurável, um job marca a
chamada `failed` com `error_message` "contato com o cliente perdido, desfecho
não confirmado" e `disconnected_by=system`. Não é apresentado como falha
confirmada pelo provedor.

### Permissões

Separadas e explícitas:

- `sip_extensions` — gestão dos ramais (admin).
- `sip_calls:read` — usar o softphone e receber chamadas.
- `sip_calls:write` — discar para números externos.

## Frontend (fase 1)

- `services/sip.ts`: encapsula o JsSIP (registro, ligar, atender, mudo,
  desligar) e expõe eventos simples. Nenhum detalhe SIP vaza para componentes.
- `stores/sip.ts`: estado do softphone. Separado do `calling.ts`.
- **Estados separados.** Registro: `conectando`, `registrado`, `recusado`,
  `reconectando`, `offline`. Chamada: `ociosa`, `chamando`, `tocando`,
  `em andamento`, `encerrando`.
- **Exclusão mútua.** Módulo `callLock` com `acquire(kind)` / `release()`
  síncronos, compartilhado por `calling.ts` e `sip.ts`. Dentro de uma aba o JS é
  single-thread, então o check-and-set é atômico.
- **Várias abas.** Só a aba que obtém `navigator.locks` (`sip-register-<userId>`)
  registra o ramal e trata chamadas. As demais mostram "ramal ativo em outra aba".
- **Toque.** Reaproveita `startRingTone` / `stopRingTone` de `calling.ts`
  (passam a ser exportados).
- **ICE.** Reaproveita `GET /api/calls/ice-servers`, validando na fase 0 que as
  credenciais TURN servem ao provedor.
- **Erros visíveis:** ramal não registrado, registro recusado, sem microfone,
  autoplay bloqueado, falha de ICE. Hoje falha de ICE some em silêncio nas
  chamadas de WhatsApp; aqui não pode.
- **UI:** discador compacto e banner de chamada recebida (atender/recusar). No
  contato, o botão de ligar oferece WhatsApp ou telefone.

## Testes obrigatórios

- Isolamento entre organizações e entre agentes (ler/atualizar log alheio → 404).
- Eventos duplicados e fora de ordem (criar 2x; `completed` antes de `answered`).
- Chamadas simultâneas: SIP + WhatsApp no mesmo atendente; dois cliques rápidos.
- Duas abas: só uma registra; a segunda assume ao fechar a primeira.
- Perda de rede: reconexão do registro; chamada em curso sem heartbeat.
- Fechamento da aba: job marca `failed` não confirmado.
- Senha nunca na resposta administrativa nem em log.
- Regressão das chamadas de WhatsApp: listagem de `call_logs`, saída e entrada.

## Riscos

- **Senha SIP no navegador** é inevitável no modelo A. Mitigação: ramal por
  atendente, senha própria do ramal, rotacionável pelo admin.
- **Dependência do provedor** para gravação e CDR. Mitigação: fase 0 antes de
  qualquer código de produto; fase 3 só com capacidade confirmada.
- **Histórico depende do navegador.** Aceito na fase 1, declarado como
  operacional.

## Itens em aberto

1. Nome do provedor brasileiro (define WSS, codecs, gravação e CDR).
2. Prazo do reaper de chamadas órfãs e intervalo do heartbeat (proposta: 30s e
   3 min sem sinal).
3. Status próprio para chamada órfã em vez de `failed` com mensagem (proposta
   atual: manter `failed`, sem novo status).
