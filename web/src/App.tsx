import { type FormEvent, useEffect, useRef, useState } from 'react'
import { createTestOrder, getTracking, NotFoundError, type Tracking } from './api'
import { ago, etaLine, formatKm, statusTitle } from './format'
import { RouteMap } from './RouteMap'
import { Steps } from './Steps'

const POLL_MS = 5000 // consulta o rastreio a cada 5 s
const IS_DEMO = import.meta.env.VITE_DEMO === 'true'
const REPO = 'https://github.com/Kaylan00/lastmile-go'

function orderFromURL(): string {
  return new URLSearchParams(window.location.search).get('pedido') ?? ''
}

function setURL(id: string) {
  const url = new URL(window.location.href)
  if (id) url.searchParams.set('pedido', id)
  else url.searchParams.delete('pedido')
  window.history.replaceState(null, '', url)
}

export default function App() {
  const [input, setInput] = useState(orderFromURL)
  const [orderId, setOrderId] = useState(orderFromURL)
  const [data, setData] = useState<Tracking | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [updatedAt, setUpdatedAt] = useState<number | null>(null)
  const [now, setNow] = useState(Date.now())
  const createdAt = useRef(0) // quando a página criou o pedido de teste

  // Consulta o rastreio a cada 5 s até a entrega terminar.
  useEffect(() => {
    if (!orderId) return
    const ctrl = new AbortController()
    let timer: number | undefined

    async function load() {
      let wait = POLL_MS
      try {
        const t = await getTracking(orderId, ctrl.signal)
        setData(t)
        setNotice(null)
        setUpdatedAt(Date.now())
        if (t.status === 'delivered' || t.status === 'failed_attempt') return // terminou: para de consultar
      } catch (err) {
        if (ctrl.signal.aborted) return
        if (err instanceof NotFoundError) {
          // Pedido recém-criado: o evento ainda está indo do dispatch para o tracking pelo Pub/Sub.
          if (Date.now() - createdAt.current < 15_000) {
            setNotice('Registrando o pedido...')
            wait = 1000
          } else {
            setData(null)
            setNotice('Pedido não encontrado. Confira o número.')
            return
          }
        } else {
          setNotice('Sem conexão com o servidor. Tentando de novo...')
        }
      }
      timer = window.setTimeout(load, wait)
    }

    setData(null)
    setNotice(null)
    load()
    return () => {
      ctrl.abort()
      window.clearTimeout(timer)
    }
  }, [orderId])

  // Atualiza o "atualizado há X s" a cada segundo.
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [])

  function track(e: FormEvent) {
    e.preventDefault()
    const id = input.trim()
    setURL(id)
    setOrderId(id)
  }

  async function createOrder() {
    setCreating(true)
    try {
      const { id } = await createTestOrder()
      createdAt.current = Date.now()
      setInput(id)
      setURL(id)
      setOrderId(id)
    } catch {
      setNotice('Não foi possível criar o pedido de teste.')
    } finally {
      setCreating(false)
    }
  }

  const line = data ? etaLine(data) : null
  const live = data && data.status !== 'delivered' && data.status !== 'failed_attempt'

  return (
    <div className="app">
      <header className="topbar">
        <a className="brand" href="/">
          <img src="/favicon.svg" alt="" width="22" height="22" />
          <span>
            lastmile<b>go</b>
          </span>
        </a>
        <a className="repo" href={REPO}>
          Código no GitHub
        </a>
      </header>

      {IS_DEMO && (
        <p className="demo-note">
          Demonstração. A API desta página roda o código Go do tracking (máquina de estados e previsão
          de chegada) com uma entrega simulada em memória, em velocidade acelerada. O sistema completo,
          com dois serviços, PostgreSQL e Pub/Sub, sobe com <code>docker compose up</code>.{' '}
          <a href={REPO + '#readme'}>Como funciona</a>
        </p>
      )}

      <main className="layout">
        <section className="panel">
          <h1>Acompanhe sua entrega</h1>
          <form className="search" onSubmit={track}>
            <label htmlFor="order">Número do pedido</label>
            <div className="search-row">
              <input
                id="order"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="Cole o número do pedido"
                autoComplete="off"
                spellCheck={false}
              />
              <button type="submit" disabled={!input.trim()}>
                Rastrear
              </button>
            </div>
          </form>
          <button className="secondary" onClick={createOrder} disabled={creating}>
            {creating ? 'Criando...' : 'Criar um pedido de teste'}
          </button>

          {notice && <p className="notice">{notice}</p>}

          {data && (
            <div className="result">
              <h2 className={data.status === 'failed_attempt' ? 'title failed' : 'title'}>
                {statusTitle[data.status]}
              </h2>
              {line && <p className="eta">{line}</p>}
              {data.message && <p className="message">{data.message}</p>}

              <Steps data={data} />

              <dl className="details">
                {data.driver_id && (
                  <>
                    <dt>Motorista</dt>
                    <dd>{data.driver_id}</dd>
                  </>
                )}
                {data.eta && (
                  <>
                    <dt>Distância restante</dt>
                    <dd>{formatKm(data.eta.distance_km)}</dd>
                  </>
                )}
                {live && data.driver_position && (
                  <>
                    <dt>Última posição</dt>
                    <dd>{ago(now - new Date(data.driver_position.at).getTime())}</dd>
                  </>
                )}
                <dt>Pedido</dt>
                <dd className="mono">{data.order_id}</dd>
              </dl>
              {updatedAt && live && (
                <p className="updated">Atualiza sozinho a cada 5 s · última consulta {ago(now - updatedAt)}</p>
              )}
            </div>
          )}
        </section>

        <section className="map-area">
          <RouteMap data={data} />
        </section>
      </main>

      <footer className="footer">
        Projeto de estudo de logística de última milha: dispatch e tracking em Go, Pub/Sub e PostgreSQL.
      </footer>
    </div>
  )
}
