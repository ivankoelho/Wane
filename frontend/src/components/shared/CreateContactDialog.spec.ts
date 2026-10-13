// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ptBR from '@/i18n/locales/pt-BR.json'
import en from '@/i18n/locales/en.json'

const api = vi.hoisted(() => ({ create: vi.fn(), listAccounts: vi.fn() }))
vi.mock('@/services/api', () => ({
  contactsService: { create: api.create },
  accountsService: { list: api.listAccounts }
}))
vi.mock('@/stores/tags', () => ({ useTagsStore: () => ({ fetchTags: vi.fn().mockResolvedValue({ tags: [] }) }) }))
const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), warning: vi.fn() }))
vi.mock('vue-sonner', () => ({ toast }))

import CreateContactDialog from './CreateContactDialog.vue'

const $ = (id: string) => document.body.querySelector<HTMLElement>(`[data-testid="${id}"]`)

// The shape axios gives for a 409/403 carrying the backend envelope.
const apiError = (status: number, data: Record<string, unknown>) =>
  Object.assign(new Error('request failed'), { isAxiosError: true, response: { status, data: { status: 'error', message: 'x', data } } })

async function open() {
  const i18n = createI18n({ legacy: false, locale: 'pt-BR', messages: { 'pt-BR': ptBR as any, en: en as any } })
  const w = mount(CreateContactDialog, { props: { open: false }, global: { plugins: [i18n] }, attachTo: document.body })
  await w.setProps({ open: true })
  await flushPromises()
  return w
}

async function typePhone(value: string) {
  const input = $('create-contact-phone') as HTMLInputElement
  input.value = value
  input.dispatchEvent(new Event('input'))
  await flushPromises()
}

async function click(id: string) {
  $(id)!.click()
  await flushPromises()
}

beforeEach(() => {
  document.body.innerHTML = ''
  vi.clearAllMocks()
  api.listAccounts.mockResolvedValue({ data: { data: { accounts: [] } } })
})

describe('CreateContactDialog duplicate handling', () => {
  it('does not allow repeating the request for a number held by someone else, and rearms on a new number', async () => {
    api.create.mockRejectedValue(apiError(409, {
      code: 'contact_exists_assigned', contact_name: 'Maria', assigned_user_name: 'Caio', assigned_to_me: false
    }))
    await open()
    await typePhone('5511900000001')
    await click('create-contact-submit')

    expect(api.create).toHaveBeenCalledTimes(1)
    expect(document.body.textContent).toContain('Maria já existe e é atendido por Caio')
    expect(($('create-contact-submit') as HTMLButtonElement).disabled).toBe(true)

    await click('create-contact-submit')
    expect(api.create).toHaveBeenCalledTimes(1)

    await typePhone('5511900000009')
    expect(document.body.textContent).not.toContain('Este número já está cadastrado')
    expect(($('create-contact-submit') as HTMLButtonElement).disabled).toBe(false)
  })

  it('explains an unassigned contact the agent cannot take and offers no claim', async () => {
    api.create.mockRejectedValue(apiError(409, {
      code: 'contact_exists_unassigned', contact_name: 'Cliente Na Fila', can_claim: false,
      claim_blocked_reason: 'pickup_permission_required'
    }))
    await open()
    await typePhone('5511900000003')
    await click('create-contact-submit')

    expect(document.body.textContent).toContain('você não pode assumi-lo')
    expect(document.body.textContent).toContain('Seu perfil não inclui retirar contatos da fila')
    expect($('create-contact-claim')).toBeNull()
    expect(($('create-contact-submit') as HTMLButtonElement).disabled).toBe(true)
    expect(api.create).toHaveBeenCalledTimes(1)
  })

  it('offers the claim for an eligible unassigned contact and resends with claim', async () => {
    api.create
      .mockRejectedValueOnce(apiError(409, { code: 'contact_exists_unassigned', contact_name: 'Sem Dono', can_claim: true }))
      .mockResolvedValueOnce({ data: { data: { id: 'c1' } } })
    await open()
    await typePhone('5511900000002')
    await click('create-contact-submit')

    expect($('create-contact-claim')).not.toBeNull()
    await click('create-contact-claim')
    expect(api.create).toHaveBeenLastCalledWith(expect.objectContaining({ phone_number: '5511900000002', claim: true }))
  })

  it('turns a refused claim (403 with a stable code) into an explanation, not a retry', async () => {
    api.create
      .mockRejectedValueOnce(apiError(409, { code: 'contact_exists_unassigned', contact_name: 'Sem Dono', can_claim: true }))
      .mockRejectedValueOnce(apiError(403, { code: 'not_team_member' }))
    await open()
    await typePhone('5511900000002')
    await click('create-contact-submit')
    await click('create-contact-claim')

    expect($('create-contact-claim')).toBeNull()
    expect(document.body.textContent).toContain('equipe da qual você não faz parte')
    expect(toast.error).not.toHaveBeenCalled()
  })
})
