// Chamadas à API. A página sempre fala com /api: no docker-compose o nginx
// repassa para os serviços; na demo publicada, /api é a função serverless.

export type Status =
  | 'pending'
  | 'assigned'
  | 'picked_up'
  | 'out_for_delivery'
  | 'delivered'
  | 'failed_attempt'

export interface Point {
  lat: number
  lng: number
}

export interface Tracking {
  order_id: string
  status: Status
  status_at?: string
  driver_id?: string
  pickup: Point
  dropoff: Point
  driver_position?: Point & { at: string }
  eta?: { distance_km: number; minutes_left: number; arrival: string }
  message?: string
  timeline: { type: string; at: string }[]
  demo?: boolean
}

export class NotFoundError extends Error {}

export async function getTracking(id: string, signal?: AbortSignal): Promise<Tracking> {
  const res = await fetch(`/api/orders/${encodeURIComponent(id)}/tracking`, { signal })
  if (res.status === 404) throw new NotFoundError('pedido não encontrado')
  if (!res.ok) throw new Error(`erro ${res.status}`)
  return res.json()
}

// Ponto de referência dos pedidos de teste: Av. Paulista, São Paulo.
const CENTER: Point = { lat: -23.5614, lng: -46.6559 }

// Ponto a `km` quilômetros de p, na direção `angle` (radianos).
function offset(p: Point, km: number, angle: number): Point {
  return {
    lat: p.lat + (km * Math.cos(angle)) / 111.32,
    lng: p.lng + (km * Math.sin(angle)) / (111.32 * Math.cos((p.lat * Math.PI) / 180)),
  }
}

// Cria um pedido com coleta perto da Paulista e entrega a 1,5-2,8 km dela.
export async function createTestOrder(): Promise<{ id: string }> {
  const pickup = offset(CENTER, Math.random() * 1.5, Math.random() * 2 * Math.PI)
  const dropoff = offset(pickup, 1.5 + Math.random() * 1.3, Math.random() * 2 * Math.PI)
  const res = await fetch('/api/orders', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pickup, dropoff }),
  })
  if (!res.ok) throw new Error(`erro ${res.status}`)
  return res.json()
}
