import type { Status, Tracking } from './api'
import { formatTime } from './format'

const rank: Record<Status, number> = {
  pending: 0,
  assigned: 1,
  picked_up: 2,
  out_for_delivery: 3,
  delivered: 4,
  failed_attempt: 4,
}

// Etapas da entrega, na ordem. A última depende de como terminou.
function stepsFor(t: Tracking) {
  const failed = t.status === 'failed_attempt'
  return [
    { event: 'order.created', rank: 0, label: 'Pedido recebido' },
    { event: 'order.assigned', rank: 1, label: 'Motorista a caminho da coleta' },
    { event: 'order.picked_up', rank: 2, label: 'Pacote coletado' },
    { event: 'order.out_for_delivery', rank: 3, label: 'Saiu para entrega' },
    failed
      ? { event: 'order.failed_attempt', rank: 4, label: 'Tentativa de entrega sem sucesso' }
      : { event: 'order.delivered', rank: 4, label: 'Entregue' },
  ]
}

export function Steps({ data }: { data: Tracking }) {
  const current = rank[data.status]
  const finished = current === 4
  const when = new Map(data.timeline.map((e) => [e.type, e.at]))

  return (
    <ol className="steps">
      {stepsFor(data).map((step) => {
        const state =
          step.rank < current || (finished && step.rank === current)
            ? 'done'
            : step.rank === current
              ? 'current'
              : 'todo'
        const at = when.get(step.event)
        const failedStep = step.event === 'order.failed_attempt'
        return (
          <li key={step.event} className={`step ${state}${failedStep ? ' failed' : ''}`}>
            <span className="dot" aria-hidden="true" />
            <span className="label">{step.label}</span>
            <time>{at ? formatTime(at) : ''}</time>
          </li>
        )
      })}
    </ol>
  )
}
