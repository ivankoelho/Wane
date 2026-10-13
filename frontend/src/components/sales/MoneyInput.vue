<script setup lang="ts">
// Text input for a BRL amount: "R$" prefix, accepts "1799,99", "1799.99" or
// "1.799,99" while typing and tidies it to "1.799,99" on blur. The model stays a
// string; callers parse it with parseDecimalInput.
import { Input } from '@/components/ui/input'
import { parseDecimalInput } from '@/lib/salesUnits'
import { formatMoneyInput } from '@/lib/currency'
import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ modelValue: string; class?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function tidy() {
  const n = parseDecimalInput(props.modelValue)
  if (n !== undefined && !Number.isNaN(n)) emit('update:modelValue', formatMoneyInput(n))
}
</script>

<template>
  <div class="relative">
    <span class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">R$</span>
    <Input
      v-bind="$attrs"
      :model-value="modelValue"
      type="text"
      inputmode="decimal"
      :class="cn('pl-9', props.class)"
      @update:model-value="emit('update:modelValue', $event)"
      @blur="tidy"
    />
  </div>
</template>
