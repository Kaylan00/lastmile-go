import type { Status, Tracking } from './api'

export const statusTitle: Record<Status, string> = {
  pending: 'Procurando um motorista',
  assigned: 'Motorista indo buscar seu pedido',
  picked_up: 'Pedido coletado',
  out_for_delivery: 'Saiu para entrega',
  delivered: 'Pedido entregue',
  failed_attempt: 'Não conseguimos entregar',
}

export const eventLabel: Record<string, string> = {
  'order.created': 'Pedido recebido',
  'order.assigned': 'Motorista definido',
  'order.picked_up': 'Pacote coletado',
  'order.out_for_delivery': 'Saiu para entrega',
  'order.delivered': 'Entregue',
  'order.failed_attempt': 'Tentativa de entrega sem sucesso',
}

const timeFmt = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' })

export function formatTime(iso: string): string {
  return timeFmt.format(new Date(iso))
}

export function formatKm(km: number): string {
  return `${km.toLocaleString('pt-BR', { maximumFractionDigits: 1 })} km`
}

// Linha logo abaixo do título: previsão, horário da entrega ou nada.
export function etaLine(t: Tracking): string | null {
  if (t.status === 'delivered' && t.status_at) return `Entregue às ${formatTime(t.status_at)}`
  if (!t.eta) return null
  if (t.eta.minutes_left <= 0) return 'Chegando agora'
  const min = t.eta.minutes_left === 1 ? '1 minuto' : `${t.eta.minutes_left} minutos`
  return `Chega em cerca de ${min}, por volta das ${formatTime(t.eta.arrival)}`
}

// "há 3 s", "há 2 min"
export function ago(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `há ${s} s`
  return `há ${Math.floor(s / 60)} min`
}
