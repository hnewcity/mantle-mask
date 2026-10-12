import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)) }
export function number(value: number) { return new Intl.NumberFormat("zh-CN", { notation: value >= 100000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value) }
export function time(value: string) { return new Date(value).toLocaleTimeString("zh-CN", { hour12: false }) }
export function date(value: string) { return new Date(value).toLocaleDateString("zh-CN") }
