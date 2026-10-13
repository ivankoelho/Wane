import { describe, expect, it } from 'vitest'
import { parseDecimalInput } from './salesUnits'

describe('parseDecimalInput', () => {
  it('accepts a comma or a dot as the decimal separator', () => {
    expect(parseDecimalInput('1799,99')).toBe(1799.99)
    expect(parseDecimalInput('1799.99')).toBe(1799.99)
    expect(parseDecimalInput(' 12,5 ')).toBe(12.5)
  })

  it('reads dots as thousands separators when there is a comma', () => {
    expect(parseDecimalInput('1.799,99')).toBe(1799.99)
    expect(parseDecimalInput('1.234.567,8')).toBe(1234567.8)
  })

  it('keeps empty as not filled in and rejects garbage or negatives', () => {
    expect(parseDecimalInput('')).toBeUndefined()
    expect(parseDecimalInput('abc')).toBeNaN()
    expect(parseDecimalInput('1,2,3')).toBeNaN()
    expect(parseDecimalInput('-5')).toBeNaN()
  })
})
