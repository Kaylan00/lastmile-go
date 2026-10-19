import { useEffect, useRef } from 'react'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import type { Point, Tracking } from './api'

const SAO_PAULO: L.LatLngTuple = [-23.5614, -46.6559]

function icon(className: string, text = '') {
  return L.divIcon({
    className: '',
    html: `<div class="pin ${className}">${text}</div>`,
    iconSize: [28, 28],
    iconAnchor: [14, 14],
  })
}

const ll = (p: Point): L.LatLngTuple => [p.lat, p.lng]

interface Layers {
  group: L.LayerGroup
  orderId: string
  hasDriver: boolean
}

// Mapa com a coleta (C), o comprador (E) e o motorista. Usa Leaflet direto,
// sem biblioteca de React por cima: o mapa é criado uma vez e só os
// marcadores mudam a cada atualização.
export function RouteMap({ data }: { data: Tracking | null }) {
  const el = useRef<HTMLDivElement>(null)
  const map = useRef<L.Map | null>(null)
  const layers = useRef<Layers | null>(null)

  useEffect(() => {
    if (!el.current) return
    const m = L.map(el.current, { zoomControl: true, attributionControl: true }).setView(SAO_PAULO, 13)
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(m)
    map.current = m
    return () => {
      m.remove()
      map.current = null
      layers.current = null
    }
  }, [])

  useEffect(() => {
    const m = map.current
    if (!m) return
    layers.current?.group.remove()
    if (!data) return

    const group = L.layerGroup().addTo(m)
    const driver = data.driver_position
    const active = data.status === 'assigned' || data.status === 'picked_up' || data.status === 'out_for_delivery'

    // trajeto que falta (cor de destaque) e o resto (cinza)
    if (driver && data.status === 'assigned') {
      L.polyline([ll(driver), ll(data.pickup)], { color: '#0b6e4f', weight: 4 }).addTo(group)
      L.polyline([ll(data.pickup), ll(data.dropoff)], { color: '#8a909c', weight: 3, opacity: 0.7 }).addTo(group)
    } else if (driver && active) {
      L.polyline([ll(driver), ll(data.dropoff)], { color: '#0b6e4f', weight: 4 }).addTo(group)
    } else {
      L.polyline([ll(data.pickup), ll(data.dropoff)], { color: '#8a909c', weight: 3, opacity: 0.7 }).addTo(group)
    }

    L.marker(ll(data.pickup), { icon: icon('pickup', 'C'), title: 'Coleta' }).addTo(group)
    L.marker(ll(data.dropoff), { icon: icon('dropoff', 'E'), title: 'Endereço de entrega' }).addTo(group)
    if (driver && active) {
      L.marker(ll(driver), { icon: icon('driver'), title: 'Motorista', zIndexOffset: 1000 }).addTo(group)
    }

    // Enquadra o trajeto quando o pedido muda ou quando o motorista aparece.
    // Nas outras atualizações o zoom fica como o usuário deixou.
    const hasDriver = Boolean(driver && active)
    const prev = layers.current
    if (prev?.orderId !== data.order_id || (hasDriver && !prev.hasDriver)) {
      const pts = [ll(data.pickup), ll(data.dropoff)]
      if (driver && hasDriver) pts.push(ll(driver))
      m.fitBounds(L.latLngBounds(pts), { padding: [48, 48], maxZoom: 16 })
    }
    layers.current = { group, orderId: data.order_id, hasDriver }
  }, [data])

  return <div ref={el} className="map" role="img" aria-label="Mapa do trajeto da entrega" />
}
