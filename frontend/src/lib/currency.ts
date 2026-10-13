// pt-BR/BRL: the sales funnel this serves (Central de Vendas, XProcess
// integration) is Brazilian-only — same one-off Intl.NumberFormat pattern
// MetaInsightsView.vue uses for its own currency, just centralized here now
// that a second component (XProcessLinkDialog) needs the identical format.
export function formatCurrency(value?: number | null): string {
  if (value == null) return '—'
  return new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' }).format(value)
}

// The editable form of an amount, without the symbol (MoneyInput shows "R$"): "1.799,99".
export function formatMoneyInput(value?: number | null): string {
  if (value == null) return ''
  return new Intl.NumberFormat('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(value)
}
