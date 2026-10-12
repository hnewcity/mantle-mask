export function formatLocalDate(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
}

export function parseLocalDate(value: string) {
  const [year, month, day] = value.split("-").map(Number)
  // Date-only values must stay on the same local day, including west of UTC.
  return new Date(year, month - 1, day)
}

export function localDate(daysAgo = 0) {
  const date = new Date()
  date.setDate(date.getDate() - daysAgo)
  return formatLocalDate(date)
}
