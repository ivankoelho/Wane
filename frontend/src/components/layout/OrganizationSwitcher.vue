<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useOrganizationsStore } from '@/stores/organizations'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { organizationsService } from '@/services/api'
import { toast } from 'vue-sonner'
import { Building2, Plus, Loader2 } from 'lucide-vue-next'

defineProps<{
  collapsed?: boolean
}>()

// Collapsed, there is no room for the picker: the icon opens the sidebar instead.
const emit = defineEmits<{ expand: [] }>()

const { t } = useI18n()
const organizationsStore = useOrganizationsStore()
const authStore = useAuthStore()

const isSuperAdmin = computed(() => authStore.user?.is_super_admin || false)
const canCreateOrg = computed(() => authStore.hasPermission('organizations', 'write'))

const shouldShowSwitcher = computed(() =>
  isSuperAdmin.value || organizationsStore.isMultiOrg
)

// Build the org list depending on user type
const orgList = computed(() => {
  if (isSuperAdmin.value) {
    return organizationsStore.organizations.map(org => ({ id: org.id, name: org.name }))
  }
  return organizationsStore.myOrganizations.map(org => ({ id: org.organization_id, name: org.name }))
})

const currentOrgId = computed(() => {
  if (isSuperAdmin.value) {
    return organizationsStore.selectedOrgId || ''
  }
  return authStore.user?.organization_id || ''
})

onMounted(async () => {
  // Fetch user's org memberships for all authenticated users
  await organizationsStore.fetchMyOrganizations()

  if (isSuperAdmin.value) {
    organizationsStore.init()
    await organizationsStore.fetchOrganizations()

    // If no org selected, default to user's own org
    if (!organizationsStore.selectedOrgId && authStore.user?.organization_id) {
      organizationsStore.selectOrganization(authStore.user.organization_id)
    }
  }
})

// Watch for auth changes
watch(() => authStore.user?.is_super_admin, async (superAdmin) => {
  if (superAdmin) {
    organizationsStore.init()
    await organizationsStore.fetchOrganizations()
  }
})

const handleOrgChange = async (value: string | number | bigint | Record<string, any> | null) => {
  if (!value || typeof value !== 'string') return

  // Everyone goes through switch-org, super admins included: the X-Organization-ID
  // header alone only scopes axios calls. The WebSocket token, <img src> media and
  // window.open previews carry no headers, so the JWT itself has to name the target
  // org or those keep talking to the previous one.
  try {
    await authStore.switchOrg(value)
    organizationsStore.selectOrganization(value)
    window.location.reload()
  } catch {
    // If switch fails, don't reload
  }
}

// Create org dialog
const isCreateDialogOpen = ref(false)
const newOrgName = ref('')
const isCreating = ref(false)

async function submitCreateOrg() {
  if (!newOrgName.value.trim()) return
  isCreating.value = true
  try {
    await organizationsService.create({ name: newOrgName.value.trim() })
    toast.success(t('organizations.created'))
    isCreateDialogOpen.value = false
    newOrgName.value = ''
    await refreshOrgs()
  } catch {
    toast.error(t('organizations.createFailed'))
  } finally {
    isCreating.value = false
  }
}

const refreshOrgs = async () => {
  if (isSuperAdmin.value) {
    await organizationsStore.fetchOrganizations()
  } else {
    await organizationsStore.fetchMyOrganizations()
  }
}
</script>

<template>
  <!-- Same 14px inset and frosted group as the nav sections below, so the
       icon and label line up with every other sidebar item. -->
  <div v-if="shouldShowSwitcher" class="px-[14px] pb-[8px]">
    <div v-if="!collapsed" class="space-y-[6px] rounded-2xl bg-white/[0.08] p-[4px]">
      <div class="flex items-center justify-between pl-[8px] pr-[2px] pt-[2px]">
        <span class="text-[11px] font-medium text-white/60">
          {{ $t('organizations.organization') }}
        </span>
        <button
          v-if="canCreateOrg"
          type="button"
          class="flex h-6 w-6 items-center justify-center rounded-lg text-white/70 transition hover:bg-white/15 hover:text-white"
          :aria-label="t('organizations.createTitle')"
          :title="t('organizations.createTitle')"
          @click="isCreateDialogOpen = true"
        >
          <Plus class="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
      <Select
        v-if="orgList.length > 0"
        :model-value="currentOrgId"
        @update:model-value="handleOrgChange"
      >
        <SelectTrigger class="h-9 rounded-xl border-white/20 bg-white/15 text-[13px] text-white [&>svg]:text-white/70">
          <SelectValue :placeholder="$t('organizations.selectOrganization')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem
            v-for="org in orgList"
            :key="org.id"
            :value="org.id"
          >
            <div class="flex items-center gap-2">
              <Building2 class="h-3.5 w-3.5 text-muted-foreground" />
              <span>{{ org.name }}</span>
            </div>
          </SelectItem>
        </SelectContent>
      </Select>
      <div v-else-if="organizationsStore.loading" class="px-[8px] pb-[4px] text-[12px] text-white/60">
        {{ $t('organizations.loading') }}
      </div>
      <div v-else-if="organizationsStore.error" class="px-[8px] pb-[4px] text-[12px] text-red-300">
        {{ organizationsStore.error }}
      </div>
      <div v-else class="px-[8px] pb-[4px] text-[12px] text-white/60">
        {{ $t('organizations.noOrganizations') }}
      </div>
    </div>

    <!-- Collapsed: a regular nav item on the shared icon axis -->
    <div v-else class="rounded-2xl bg-white/[0.08] p-[4px]">
      <button
        type="button"
        class="shell-nav-item btn-press justify-center !px-0"
        :title="organizationsStore.selectedOrganization?.name || $t('organizations.organization')"
        :aria-label="`${$t('organizations.organization')}: ${organizationsStore.selectedOrganization?.name ?? ''}`"
        @click="emit('expand')"
      >
        <Building2 class="h-[18px] w-[18px] shrink-0" aria-hidden="true" />
      </button>
    </div>
  </div>

  <!-- Create Org Dialog -->
  <Dialog v-model:open="isCreateDialogOpen">
    <DialogContent class="max-w-sm">
      <DialogHeader>
        <DialogTitle>{{ t('organizations.createTitle') }}</DialogTitle>
        <DialogDescription>{{ t('organizations.createDesc') }}</DialogDescription>
      </DialogHeader>
      <div class="py-4">
        <Input
          v-model="newOrgName"
          :placeholder="t('organizations.namePlaceholder')"
          @keydown.enter="submitCreateOrg"
        />
      </div>
      <DialogFooter>
        <Button variant="outline" @click="isCreateDialogOpen = false">{{ t('common.cancel') }}</Button>
        <Button @click="submitCreateOrg" :disabled="isCreating || !newOrgName.trim()">
          <Loader2 v-if="isCreating" class="h-4 w-4 mr-2 animate-spin" />
          {{ t('common.create') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
