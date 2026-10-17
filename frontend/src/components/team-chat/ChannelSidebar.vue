<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Hash, Users, Plus, MessageCircle } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ScrollArea } from '@/components/ui/scroll-area'
import { ErrorState } from '@/components/shared'
import { useTeamChatStore } from '@/stores/teamChat'
import { useAuthStore } from '@/stores/auth'
import type { TeamChatChannel } from '@/services/api'

const props = defineProps<{ activeId: string | null }>()
const emit = defineEmits<{
  select: [channelId: string]
  newChannel: []
  newDm: []
}>()

const { t } = useI18n()
const store = useTeamChatStore()
const auth = useAuthStore()

// Buttons mirror what the server allows; the server still decides.
const canManage = computed(() => auth.hasPermission('team_chat', 'manage'))
const canWrite = computed(() => auth.hasPermission('team_chat', 'write'))

const sections = computed(() => [
  { key: 'team', label: t('teamChat.sectionTeams'), items: store.channels.filter(c => c.kind === 'team') },
  { key: 'channel', label: t('teamChat.sectionChannels'), items: store.channels.filter(c => c.kind === 'channel') },
  { key: 'dm', label: t('teamChat.sectionDirect'), items: store.channels.filter(c => c.kind === 'dm') }
])

function isOnline(c: TeamChatChannel): boolean {
  return !!c.dm_user_id && !!store.users.find(u => u.id === c.dm_user_id)?.is_online
}

function badge(n: number): string {
  return n > 99 ? '99+' : String(n)
}
</script>

<template>
  <aside class="flex h-full w-full flex-col border-r bg-card md:w-72 md:shrink-0" :aria-label="t('teamChat.title')">
    <div class="flex items-center justify-between border-b px-4 py-3">
      <h2 class="text-sm font-semibold">{{ t('teamChat.title') }}</h2>
      <div class="flex gap-1">
        <Button
          v-if="canWrite"
          variant="ghost"
          size="icon"
          class="h-7 w-7"
          :title="t('teamChat.newDm')"
          :aria-label="t('teamChat.newDm')"
          @click="emit('newDm')"
        >
          <MessageCircle class="h-4 w-4" />
        </Button>
        <Button
          v-if="canManage"
          variant="ghost"
          size="icon"
          class="h-7 w-7"
          :title="t('teamChat.newChannel')"
          :aria-label="t('teamChat.newChannel')"
          @click="emit('newChannel')"
        >
          <Plus class="h-4 w-4" />
        </Button>
      </div>
    </div>

    <ErrorState
      v-if="store.channelsError && store.channels.length === 0"
      :title="t('teamChat.loadFailedTitle')"
      :description="t('teamChat.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="store.fetchChannels()"
    />

    <div v-else-if="store.channelsLoading && store.channels.length === 0" class="space-y-2 p-3">
      <Skeleton v-for="i in 5" :key="i" class="h-9 w-full" />
    </div>

    <ScrollArea v-else class="flex-1">
      <p v-if="store.channels.length === 0" class="px-4 py-8 text-center text-sm text-muted-foreground">
        {{ t('teamChat.noChannels') }}
      </p>
      <nav v-else class="space-y-4 p-2">
        <section v-for="s in sections" :key="s.key" v-show="s.items.length > 0">
          <h3 class="px-2 pb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{{ s.label }}</h3>
          <ul>
            <li v-for="c in s.items" :key="c.id">
              <button
                type="button"
                :data-testid="`channel-${c.id}`"
                :class="[
                  'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
                  props.activeId === c.id ? 'bg-muted font-medium' : ''
                ]"
                @click="emit('select', c.id)"
              >
                <Users v-if="c.kind === 'team'" class="h-4 w-4 shrink-0 text-muted-foreground" />
                <Hash v-else-if="c.kind === 'channel'" class="h-4 w-4 shrink-0 text-muted-foreground" />
                <span
                  v-else
                  :class="['h-2.5 w-2.5 shrink-0 rounded-full', isOnline(c) ? 'bg-emerald-500' : 'bg-muted-foreground/40']"
                  :title="isOnline(c) ? t('teamChat.online') : t('teamChat.offline')"
                />
                <span class="min-w-0 flex-1 truncate">{{ c.name || t('teamChat.unnamed') }}</span>
                <span
                  v-if="c.unread_count > 0"
                  class="min-w-5 rounded-full bg-primary px-1.5 text-center text-[11px] font-semibold leading-5 text-primary-foreground"
                  :aria-label="t('teamChat.unreadCount', { n: c.unread_count })"
                >{{ badge(c.unread_count) }}</span>
              </button>
            </li>
          </ul>
        </section>
      </nav>
    </ScrollArea>
  </aside>
</template>
