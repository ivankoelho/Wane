import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  teamChatService,
  type TeamChatChannel,
  type TeamChatMessage,
  type TeamChatUser
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import {
  TEAM_CHAT_PAGE_SIZE,
  TEAM_CHAT_TYPING_MS,
  TEAM_CHAT_TYPING_THROTTLE_MS,
  appendUnique,
  mergeLatest
} from '@/lib/team-chat'

// 'forbidden' covers both "does not exist" and "not yours": the server answers
// 404 for both on purpose, so the UI never learns which.
export type TeamChatPanelError = null | 'forbidden' | 'failed'

export interface TeamChatPanel {
  messages: TeamChatMessage[]
  hasMore: boolean
  loading: boolean
  loadingOlder: boolean
  error: TeamChatPanelError
}

interface ChatEventPayload {
  channel_id: string
  message_id?: string
  user_id: string
}

const emptyPanel = (): TeamChatPanel => ({
  messages: [], hasMore: false, loading: false, loadingOlder: false, error: null
})

const unwrap = <T>(res: { data: unknown }): T => ((res.data as any).data ?? res.data) as T

function httpStatus(err: unknown): number | undefined {
  return (err as { response?: { status?: number } })?.response?.status
}

/**
 * Internal chat state. The server is the only authority: this store never
 * decides who may see a channel. It lists what the API returns, asks for what
 * the user opens, and treats a 404 as "no access". Events carry ids only; the
 * content always comes from REST, and a reconnect re-reads everything.
 */
export const useTeamChatStore = defineStore('teamChat', () => {
  const channels = ref<TeamChatChannel[]>([])
  const channelsLoading = ref(false)
  const channelsError = ref(false)
  const activeChannelId = ref<string | null>(null)
  const panels = ref<Record<string, TeamChatPanel>>({})
  const users = ref<TeamChatUser[]>([])
  const typing = ref<Record<string, Record<string, number>>>({})

  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  // Set once the chat screen has been opened: users who never open it (or lack
  // the permission) must not trigger chat requests on every reconnect.
  let started = false
  const lastTypingSent: Record<string, number> = {}

  const myId = () => useAuthStore().user?.id

  const activeChannel = computed(() => channels.value.find(c => c.id === activeChannelId.value) ?? null)
  const activePanel = computed(() => (activeChannelId.value ? panels.value[activeChannelId.value] ?? null : null))
  const totalUnread = computed(() => channels.value.reduce((n, c) => n + (c.unread_count || 0), 0))
  const userNames = computed(() => Object.fromEntries(users.value.map(u => [u.id, u.full_name])))

  function panel(id: string): TeamChatPanel {
    if (!panels.value[id]) panels.value[id] = emptyPanel()
    return panels.value[id]
  }

  async function fetchChannels() {
    started = true
    channelsLoading.value = true
    try {
      const data = unwrap<{ channels: TeamChatChannel[] }>(await teamChatService.channels())
      channels.value = data.channels || []
      channelsError.value = false
    } catch {
      channelsError.value = true
    } finally {
      channelsLoading.value = false
    }
  }

  /** Coalesces bursts of events into one list reload. */
  function refreshChannelsSoon() {
    if (refreshTimer) return
    refreshTimer = setTimeout(() => {
      refreshTimer = null
      void fetchChannels()
    }, 250)
  }

  async function fetchUsers(q?: string) {
    try {
      users.value = unwrap<{ users: TeamChatUser[] }>(await teamChatService.users(q)).users || []
    } catch {
      users.value = []
    }
  }

  async function loadMessages(id: string) {
    const p = panel(id)
    p.loading = true
    p.error = null
    try {
      const data = unwrap<{ messages: TeamChatMessage[]; has_more: boolean }>(
        await teamChatService.messages(id, { limit: TEAM_CHAT_PAGE_SIZE })
      )
      p.messages = data.messages || []
      p.hasMore = !!data.has_more
    } catch (err) {
      p.messages = []
      p.hasMore = false
      p.error = httpStatus(err) === 404 ? 'forbidden' : 'failed'
    } finally {
      p.loading = false
    }
  }

  async function loadOlder(id: string) {
    const p = panel(id)
    if (p.loadingOlder || !p.hasMore || p.messages.length === 0) return
    p.loadingOlder = true
    try {
      const data = unwrap<{ messages: TeamChatMessage[]; has_more: boolean }>(
        await teamChatService.messages(id, { limit: TEAM_CHAT_PAGE_SIZE, before_id: p.messages[0].id })
      )
      p.messages = [...(data.messages || []), ...p.messages]
      p.hasMore = !!data.has_more
    } catch {
      // keep what we have; the next scroll retries
    } finally {
      p.loadingOlder = false
    }
  }

  /** Re-reads the latest page and folds it in (events, reconnect). */
  async function refreshLatest(id: string) {
    const p = panel(id)
    try {
      const data = unwrap<{ messages: TeamChatMessage[]; has_more: boolean }>(
        await teamChatService.messages(id, { limit: TEAM_CHAT_PAGE_SIZE })
      )
      p.messages = mergeLatest(p.messages, data.messages || [], !!data.has_more)
      if (!data.has_more) p.hasMore = false
      p.error = null
    } catch (err) {
      if (httpStatus(err) === 404) {
        p.messages = []
        p.error = 'forbidden'
      }
    }
  }

  async function markRead(id: string) {
    const ch = channels.value.find(c => c.id === id)
    if (ch) ch.unread_count = 0
    try {
      await teamChatService.markRead(id)
    } catch {
      // the next channel reload corrects the badge
    }
  }

  async function selectChannel(id: string | null) {
    activeChannelId.value = id
    if (!id) return
    await loadMessages(id)
    if (panels.value[id]?.error === null) await markRead(id)
  }

  async function send(id: string, body: string) {
    const msg = unwrap<TeamChatMessage>(await teamChatService.send(id, body))
    const p = panel(id)
    p.messages = appendUnique(p.messages, msg)
    const ch = channels.value.find(c => c.id === id)
    if (ch) ch.last_message_at = msg.created_at
    return msg
  }

  async function edit(msg: TeamChatMessage, body: string) {
    const updated = unwrap<TeamChatMessage>(await teamChatService.edit(msg.id, body))
    const p = panel(msg.channel_id)
    p.messages = p.messages.map(m => (m.id === updated.id ? updated : m))
  }

  async function remove(msg: TeamChatMessage) {
    await teamChatService.remove(msg.id)
    const p = panel(msg.channel_id)
    p.messages = p.messages.filter(m => m.id !== msg.id)
  }

  async function createChannel(name: string, memberIds: string[]) {
    const ch = unwrap<TeamChatChannel>(await teamChatService.createChannel({ name, member_ids: memberIds }))
    await fetchChannels()
    return ch
  }

  async function openDM(userId: string) {
    const ch = unwrap<TeamChatChannel>(await teamChatService.openDM(userId))
    await fetchChannels()
    return ch
  }

  /** Tells the channel the user is typing; at most once every few seconds. */
  function notifyTyping(id: string) {
    const now = Date.now()
    if (now - (lastTypingSent[id] ?? 0) < TEAM_CHAT_TYPING_THROTTLE_MS) return
    lastTypingSent[id] = now
    teamChatService.typing(id).catch(() => {})
  }

  function typingUserIds(id: string): string[] {
    return Object.keys(typing.value[id] ?? {})
  }

  function applyTyping(channelId: string, userId: string) {
    if (userId === myId()) return
    const until = Date.now() + TEAM_CHAT_TYPING_MS
    typing.value[channelId] = { ...(typing.value[channelId] ?? {}), [userId]: until }
    setTimeout(() => {
      const cur = typing.value[channelId]
      if (cur && cur[userId] === until) {
        const rest = { ...cur }
        delete rest[userId]
        typing.value[channelId] = rest
      }
    }, TEAM_CHAT_TYPING_MS)
  }

  function applyPresence(userId: string, online: boolean) {
    const u = users.value.find(x => x.id === userId)
    if (u) u.is_online = online
  }

  /**
   * Handles a team_chat_* event. Ids only: anything that needs content goes back
   * to REST, where access is checked again. A channel the user cannot open yields
   * a 404 there and ends in the "no access" state, whatever the event said.
   */
  function applyEvent(type: string, payload: ChatEventPayload) {
    const id = payload.channel_id
    const isActive = activeChannelId.value === id
    switch (type) {
      case 'team_chat_channel':
        refreshChannelsSoon()
        break
      case 'team_chat_message':
        if (isActive) {
          const known = payload.message_id && panels.value[id]?.messages.some(m => m.id === payload.message_id)
          if (!known) {
            void refreshLatest(id).then(() => {
              const visible = typeof document === 'undefined' || document.visibilityState === 'visible'
              if (payload.user_id !== myId() && visible) void markRead(id)
            })
          }
        }
        refreshChannelsSoon()
        break
      case 'team_chat_message_updated':
      case 'team_chat_message_deleted':
        if (isActive) void refreshLatest(id)
        else delete panels.value[id] // stale: reloaded when the channel is reopened
        break
      case 'team_chat_read': {
        // Another tab of this same user read the channel.
        const ch = channels.value.find(c => c.id === id)
        if (ch) ch.unread_count = 0
        break
      }
      case 'team_chat_typing':
        applyTyping(id, payload.user_id)
        break
    }
  }

  /** After a reconnect events may have been missed, so re-read the lists. */
  async function resync() {
    if (!started) return
    await fetchChannels()
    if (activeChannelId.value) await refreshLatest(activeChannelId.value)
  }

  return {
    channels, channelsLoading, channelsError, activeChannelId, panels, users, typing,
    activeChannel, activePanel, totalUnread, userNames,
    panel, fetchChannels, fetchUsers, selectChannel, loadMessages, loadOlder, refreshLatest,
    markRead, send, edit, remove, createChannel, openDM, notifyTyping, typingUserIds,
    applyPresence, applyEvent, resync
  }
})
