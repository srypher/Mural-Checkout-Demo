import { useEffect, useMemo, useState } from 'react'
import { Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { FiShoppingBag, FiLogIn, FiLogOut, FiUser, FiGrid, FiCopy, FiCheck } from 'react-icons/fi'
import axios from 'axios'

type Role = 'guest' | 'admin' | null

type AuthState = {
  token: string | null
  role: Role
}

type Product = {
  id: string
  name: string
  description: string
  priceUsdc: number
  imageUrl?: string
}

type OrderItem = {
  productId: string
  name: string
  priceUsdc: number
  quantity: number
}

type Order = {
  id: string
  customerName: string
  customerEmail?: string
  items: OrderItem[]
  amountUsdc: number
  amountCop: number
  status: string
  createdAt: string
}

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'

const STORAGE_KEY = 'mural-auth'

function useAuth(): [AuthState, (next: AuthState) => void, () => void] {
  const [auth, setAuth] = useState<AuthState>(() => {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { token: null, role: null }
    try {
      return JSON.parse(raw)
    } catch {
      return { token: null, role: null }
    }
  })

  const update = (next: AuthState) => {
    setAuth(next)
    if (next.token) {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
    } else {
      localStorage.removeItem(STORAGE_KEY)
    }
  }

  const logout = () => update({ token: null, role: null })

  return [auth, update, logout]
}

function Navbar({
  auth,
  onLogout,
}: {
  auth: AuthState
  onLogout: () => void
}) {
  const location = useLocation()
  const navigate = useNavigate()

  const isAdminRoute = location.pathname.startsWith('/admin')

  return (
    <header className="sticky top-0 z-20 border-b border-slate-200/70 bg-white/80 backdrop-blur-xl">
      <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3 md:px-6">
        <button
          onClick={() => navigate('/')}
          className="flex items-center gap-2 text-sm font-semibold tracking-tight text-slate-900"
        >
          <span className="flex h-9 w-9 items-center justify-center rounded-2xl bg-gradient-to-br from-[#FFBF00] via-[#FFE642] to-[#FF7900] shadow-lg shadow-orange-300/70">
            <FiShoppingBag className="text-slate-950" />
          </span>
          <span className="hidden flex-col text-left sm:flex">
            <span className="text-xs uppercase tracking-[0.2em] text-[#FF7900]/80">
              Mural
            </span>
            <span className="text-sm text-slate-800">Checkout Studio</span>
          </span>
        </button>

        <div className="flex items-center gap-2 text-xs md:gap-3">
          {auth.role === 'admin' && (
            <>
              <button
                onClick={() => navigate(isAdminRoute ? '/' : '/admin')}
                className="inline-flex items-center gap-1 rounded-full border border-slate-200 bg-white px-3 py-1.5 text-[11px] font-medium text-slate-700 shadow-sm shadow-slate-200/80 backdrop-blur-xs transition hover:border-slate-400 hover:bg-slate-50"
              >
                <FiGrid className="text-xs" />
                {isAdminRoute ? 'View storefront' : 'Admin dashboard'}
              </button>
            </>
          )}

          {auth.role ? (
            <>
              <span className="hidden items-center gap-1 rounded-full border border-slate-200 bg-white px-2.5 py-1 text-[11px] font-medium text-slate-700 shadow-sm backdrop-blur-xs sm:inline-flex">
                <FiUser className="text-xs text-[#FF7900]" />
                {auth.role === 'admin' ? 'Admin' : 'Customer'}
              </span>
              <button
                onClick={onLogout}
                className="inline-flex items-center gap-1.5 rounded-full border border-slate-300 bg-white px-3 py-1.5 text-[11px] font-medium text-slate-700 shadow-sm shadow-slate-200/70 backdrop-blur-xs transition hover:border-rose-300 hover:bg-rose-50 hover:text-rose-700"
              >
                <FiLogOut className="text-xs" />
                Logout
              </button>
            </>
          ) : (
            <button
              onClick={() => navigate('/login', { state: { from: location.pathname } })}
              className="inline-flex items-center gap-1.5 rounded-full border border-[#FFBF00]/70 bg-gradient-to-r from-[#FFBF00] via-[#FFE642] to-[#FF7900] px-3 py-1.5 text-[11px] font-semibold text-slate-900 shadow-sm shadow-orange-300/70 backdrop-blur-xs transition hover:brightness-110"
            >
              <FiLogIn className="text-xs" />
              Login
            </button>
          )}
        </div>
      </div>
    </header>
  )
}

function Shell({
  auth,
  onLogout,
  children,
}: {
  auth: AuthState
  onLogout: () => void
  children: React.ReactNode
}) {
  return (
    <div className="flex min-h-screen flex-col text-slate-900">
      <Navbar auth={auth} onLogout={onLogout} />
      <main className="mx-auto flex w-full max-w-6xl flex-1 flex-col px-4 pb-10 pt-6 md:px-6 md:pt-8">
        {children}
      </main>
    </div>
  )
}

function CatalogPage({ auth }: { auth: AuthState }) {
  const [products, setProducts] = useState<Product[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [cart, setCart] = useState<OrderItem[]>([])
  const [customerName, setCustomerName] = useState('')
  const [customerEmail, setCustomerEmail] = useState('')
  const [checkingOut, setCheckingOut] = useState(false)
  const [checkoutOpen, setCheckoutOpen] = useState(false)
  const [orderId, setOrderId] = useState<string | null>(null)
  const [orderStatus, setOrderStatus] = useState<string | null>(null)
  const [amountUsdc, setAmountUsdc] = useState<number | null>(null)
  const [amountCop, setAmountCop] = useState<number | null>(null)
  const [depositAddress, setDepositAddress] = useState<string | null>(null)
  const [network, setNetwork] = useState<string | null>(null)
  const [activeProduct, setActiveProduct] = useState<Product | null>(null)
  const [productModalOpen, setProductModalOpen] = useState(false)
  const [copiedAddress, setCopiedAddress] = useState(false)

  const navigate = useNavigate()

  useEffect(() => {
    const fetchProducts = async () => {
      try {
        setLoading(true)
        const res = await axios.get<Product[]>(`${API_BASE}/api/products`)
        setProducts(res.data)
      } catch (e) {
        setError('Unable to load products right now.')
      } finally {
        setLoading(false)
      }
    }
    fetchProducts()
  }, [])

  const total = useMemo(
    () => cart.reduce((sum, it) => sum + it.priceUsdc * it.quantity, 0),
    [cart],
  )

  const requireLogin = () => {
    if (!auth.role) {
      navigate('/login', { state: { from: '/' } })
      return false
    }
    return true
  }

  const addToCart = (product: Product) => {
    if (!requireLogin()) return
    setCart((prev) => {
      const existing = prev.find((it) => it.productId === product.id)
      if (existing) {
        return prev.map((it) =>
          it.productId === product.id ? { ...it, quantity: it.quantity + 1 } : it,
        )
      }
      return [
        ...prev,
        {
          productId: product.id,
          name: product.name,
          priceUsdc: product.priceUsdc,
          quantity: 1,
        },
      ]
    })
  }

  const updateQty = (id: string, delta: number) => {
    setCart((prev) =>
      prev
        .map((it) =>
          it.productId === id ? { ...it, quantity: it.quantity + delta } : it,
        )
        .filter((it) => it.quantity > 0),
    )
  }

  const startCheckout = async () => {
    if (!requireLogin()) return
    if (!customerName.trim() || !customerEmail.trim()) {
      alert('Please enter a name and email.')
      return
    }
    if (cart.length === 0) {
      alert('Your cart is empty.')
      return
    }
    try {
      setCheckingOut(true)
      const res = await axios.post(`${API_BASE}/api/orders`, {
        customerName,
        customerEmail,
        items: cart,
      })
      const data = res.data as {
        orderId: string
        amountUsdc: number
        depositAddress: string
        network: string
      }
      setOrderId(data.orderId)
      setDepositAddress(data.depositAddress)
      setCopiedAddress(false)
      setNetwork(data.network)
      setAmountUsdc(data.amountUsdc)
      setOrderStatus('pending_payment')
    } catch (e) {
      alert('Unable to start checkout. Please try again.')
    } finally {
      setCheckingOut(false)
    }
  }

  useEffect(() => {
    if (!orderId) return
    const interval = setInterval(async () => {
      try {
        const res = await axios.get<Order>(`${API_BASE}/api/orders/${orderId}`)
        setOrderStatus(res.data.status)
        setAmountCop(res.data.amountCop)
      } catch {
        // noop for demo
      }
    }, 4000)
    return () => clearInterval(interval)
  }, [orderId])

  // Animate product modal open/close so it "grows" out of the grid
  useEffect(() => {
    if (activeProduct) {
      // first render closed, then open on next frame to trigger CSS transition
      setProductModalOpen(false)
      const id = requestAnimationFrame(() => setProductModalOpen(true))
      return () => cancelAnimationFrame(id)
    }
    setProductModalOpen(false)
  }, [activeProduct])

  // Persist a separate cart per auth role (guest/admin) and clear when logged out
  useEffect(() => {
    if (typeof window === 'undefined') return
    if (!auth.role) return
    const key = `mural-cart-${auth.role}`
    try {
      const raw = window.localStorage.getItem(key)
      if (raw) {
        setCart(JSON.parse(raw))
      } else {
        setCart([])
      }
    } catch {
      // ignore load errors, keep existing cart in memory
    }
  }, [auth.role])

  useEffect(() => {
    if (typeof window === 'undefined') return
    if (!auth.role) return
    const key = `mural-cart-${auth.role}`
    try {
      window.localStorage.setItem(key, JSON.stringify(cart))
    } catch {
      // ignore persistence errors
    }
  }, [cart, auth.role])

  return (
    <>
      <div className="grid gap-6 md:gap-8">
        <section className="space-y-5">
          <div className="space-y-2">
            <h1 className="text-balance text-2xl font-semibold tracking-tight text-slate-900 md:text-3xl">
              Curated USDC checkout flow
            </h1>
            <p className="max-w-xl text-sm text-slate-700">
              Browse a focused catalog, pay in{' '}
              <span className="font-semibold text-[#FF7900]">USDC on Polygon</span>, and
              watch funds move automatically into{' '}
              <span className="font-semibold text-[#FFBF00]">COP</span> for the merchant.
            </p>
            <div className="h-px w-28 bg-gradient-to-r from-[#FFBF00] via-[#F2CF7E] to-[#FF7900]" />
          </div>

          <div className="rounded-3xl bg-slate-200/60 p-px">
            <div className="grid grid-cols-1 gap-px bg-slate-200 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
              {loading &&
                [1, 2, 3, 4, 5, 6].map((i) => (
                  <div
                    key={i}
                    className="flex flex-col bg-white p-5 text-xs text-slate-600 backdrop-blur-xl"
                  >
                    <div className="aspect-[4/3] w-full animate-pulse rounded-2xl bg-slate-200/80" />
                    <div className="mt-3 h-4 w-24 animate-pulse rounded-full bg-slate-200/80" />
                    <div className="mt-2 h-3 w-32 animate-pulse rounded-full bg-slate-100/80" />
                    <div className="mt-4 h-8 w-20 animate-pulse rounded-full bg-slate-200/90" />
                  </div>
                ))}

              {!loading &&
                !error &&
                products.map((p) => (
                  <article
                    key={p.id}
                    onClick={() => {
                      setActiveProduct(p)
                    }}
                    className="group flex cursor-pointer flex-col bg-white p-5 text-xs text-slate-600 shadow-[0_0_0_rgba(0,0,0,0)] backdrop-blur-xl transition-transform duration-200 ease-out hover:z-10 hover:scale-[1.03] hover:bg-[linear-gradient(135deg,#fff9e6_0%,#fef3c7_40%,#fde68a_100%)] hover:shadow-[0_20px_45px_rgba(250,204,21,0.45)]"
                  >
                    <div className="overflow-hidden rounded-2xl">
                      <img
                        src={p.imageUrl || 'https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg'}
                        alt={p.name}
                        className="aspect-[4/3] w-full object-cover transition-transform duration-200 ease-out group-hover:scale-105"
                      />
                    </div>
                    <div className="mt-3 flex flex-1 flex-col">
                      <h2 className="text-sm font-semibold text-slate-900 md:text-base">
                        {p.name}
                      </h2>
                      <p className="mt-1 line-clamp-2 text-xs text-slate-500">
                        {p.description}
                      </p>
                    </div>
                    <div className="mt-4 flex items-center justify-between">
                      <span className="text-sm font-semibold text-[#FF7900] md:text-base">
                        {p.priceUsdc.toFixed(2)} USDC
                      </span>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          addToCart(p)
                        }}
                        className="inline-flex items-center gap-1.5 rounded-full border border-[#FFBF00]/60 bg-[#FFF7DA] px-3 py-1.5 text-[11px] font-semibold text-[#B86400] shadow-sm shadow-orange-200/70 transition hover:border-[#FF7900] hover:bg-[#FFE642]"
                      >
                        <FiShoppingBag className="text-xs" />
                        Add to cart
                      </button>
                    </div>
                  </article>
                ))}
            </div>

            {error && (
              <div className="mt-3 rounded-3xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700 shadow-[0_14px_40px_rgba(248,113,113,0.25)]">
                {error}
              </div>
            )}
          </div>
        </section>
      </div>

      {activeProduct && (
        <div
          className={`fixed inset-0 z-40 flex items-center justify-center bg-slate-900/40 px-4 pb-10 pt-20 backdrop-blur-2xl transition-opacity duration-200 ease-out ${
            productModalOpen ? 'opacity-100' : 'opacity-0'
          }`}
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              setActiveProduct(null)
            }
          }}
        >
          <div
            className={`relative w-full max-w-4xl transform rounded-3xl border border-slate-200/80 bg-white p-5 text-xs text-slate-800 shadow-[0_26px_80px_rgba(15,23,42,0.45)] transition-all duration-200 ease-out md:p-7 ${
              productModalOpen ? 'scale-100 opacity-100 translate-y-0' : 'scale-95 opacity-0 -translate-y-3'
            }`}
          >
            <button
              type="button"
              onClick={() => {
                setActiveProduct(null)
              }}
              className="absolute right-4 top-4 rounded-full border border-slate-200 bg-white/90 px-2 py-1 text-[11px] text-slate-600 shadow-sm hover:border-slate-400 hover:bg-white"
            >
              Close
            </button>
            <div className="grid gap-5 md:grid-cols-[minmax(0,1.7fr)_minmax(0,1.3fr)] md:items-start">
              <div className="overflow-hidden rounded-2xl bg-slate-100">
                <img
                  src={
                    activeProduct.imageUrl ||
                    'https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg'
                  }
                  alt={activeProduct.name}
                  className="max-h-[420px] w-full object-cover md:max-h-[460px]"
                />
              </div>
              <div className="space-y-2">
                <h2 className="text-base font-semibold text-slate-900 md:text-lg">
                  {activeProduct.name}
                </h2>
                <p className="text-xs text-slate-600 md:text-sm">
                  {activeProduct.description}
                </p>
                <p className="pt-2 text-sm font-semibold text-[#FF7900] md:text-base">
                  {activeProduct.priceUsdc.toFixed(2)} USDC
                </p>
                <button
                  type="button"
                  onClick={() => {
                    addToCart(activeProduct)
                    setActiveProduct(null)
                  }}
                  className="mt-4 inline-flex items-center justify-center gap-2 rounded-2xl bg-gradient-to-r from-[#FFBF00] via-[#FFE642] to-[#FF7900] px-5 py-2.5 text-[11px] font-semibold text-slate-900 shadow-md shadow-orange-300/70 hover:brightness-110 md:text-xs"
                >
                  <FiShoppingBag className="text-xs" />
                  Add to cart
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {auth.role && cart.length > 0 && (
        <button
          onClick={() => setCheckoutOpen(true)}
          className="fixed bottom-5 right-5 z-30 inline-flex h-12 w-12 items-center justify-center rounded-full border border-[#FFBF00]/80 bg-white/95 text-[#B86400] shadow-[0_18px_50px_rgba(249,115,22,0.3)] backdrop-blur-xl transition hover:border-[#FF7900] hover:bg-[#FFF7DA] hover:shadow-[0_22px_60px_rgba(249,115,22,0.45)] md:bottom-6 md:right-6"
          aria-label="Open checkout"
        >
          <span className="relative flex h-7 w-7 items-center justify-center rounded-full bg-gradient-to-br from-[#FFBF00] via-[#FFE642] to-[#FF7900] text-slate-900 shadow-md shadow-orange-300/70">
            <FiShoppingBag className="text-sm" />
            <span className="absolute -right-2 -top-2 flex h-4 min-w-[1.1rem] items-center justify-center rounded-full bg-[#FF7900] px-1 text-[10px] font-semibold text-white shadow-sm shadow-orange-400">
              {cart.length}
            </span>
          </span>
        </button>
      )}

      {checkoutOpen && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-slate-900/15 px-4 pb-10 pt-20 backdrop-blur-2xl">
          <div className="relative w-full max-w-lg rounded-3xl border border-slate-200/80 bg-gradient-to-br from-white/95 via-slate-50/95 to-slate-100/90 p-5 shadow-[0_26px_90px_rgba(15,23,42,0.35)]">
            <button
              onClick={() => setCheckoutOpen(false)}
              className="absolute right-4 top-4 rounded-full border border-slate-200 bg-white/80 px-2 py-1 text-[11px] text-slate-600 shadow-sm hover:border-slate-400 hover:bg-white"
            >
              Close
            </button>

            <h2 className="pr-16 text-sm font-semibold uppercase tracking-[0.2em] text-slate-500">
              Checkout
            </h2>
            <p className="mt-1 text-xs text-slate-500">
              Confirm your cart and send a single{' '}
              <span className="font-semibold text-[#FF7900]">USDC</span> transfer
              on Polygon to complete this order.
            </p>

            <div className="mt-4 max-h-[45vh] space-y-3 overflow-y-auto pr-1 text-xs">
              <div>
                <h3 className="text-[11px] font-medium uppercase tracking-[0.16em] text-slate-500">
                  Cart summary
                </h3>
                <div className="mt-2 space-y-1.5">
                  {cart.map((item) => (
                    <div
                      key={item.productId}
                      className="flex items-center justify-between rounded-2xl bg-slate-100 px-2 py-2 text-xs text-slate-900"
                    >
                      <div>
                        <div className="font-medium">{item.name}</div>
                        <div className="mt-0.5 text-[11px] text-slate-500">
                          {item.priceUsdc.toFixed(2)} USDC × {item.quantity}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          onClick={() => updateQty(item.productId, -1)}
                          className="flex h-6 w-6 items-center justify-center rounded-full bg-white text-xs text-slate-600 shadow-sm"
                        >
                          -
                        </button>
                        <span className="text-[11px] tabular-nums text-slate-900">
                          {item.quantity}
                        </span>
                        <button
                          onClick={() => updateQty(item.productId, 1)}
                          className="flex h-6 w-6 items-center justify-center rounded-full bg-white text-xs text-slate-600 shadow-sm"
                        >
                          +
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="mt-3 border-t border-slate-200 pt-2 text-xs text-slate-700">
                  <div className="flex items-center justify-between">
                    <span>Total due</span>
                    <span className="font-semibold text-[#FF7900]">
                      {total.toFixed(2)} USDC
                    </span>
                  </div>
                </div>
              </div>

              <div className="grid gap-2 text-xs">
                <div className="grid gap-1.5">
                  <label className="text-[11px] font-medium text-slate-600">
                    Customer name
                  </label>
                  <input
                    value={customerName}
                    onChange={(e) => setCustomerName(e.target.value)}
                    className="h-8 rounded-xl border border-slate-200 bg-white px-3 text-xs text-slate-900 shadow-inner shadow-slate-200/80 outline-none ring-0 transition focus:border-[#FFBF00]/70 focus:bg-[#FFF7DA]"
                    placeholder="Jane Doe"
                  />
                </div>
                <div className="grid gap-1.5">
                  <label className="text-[11px] font-medium text-slate-600">
                    Email for receipt
                  </label>
                  <input
                    type="email"
                    value={customerEmail}
                    onChange={(e) => setCustomerEmail(e.target.value)}
                    className="h-8 rounded-xl border border-slate-200 bg-white px-3 text-xs text-slate-900 shadow-inner shadow-slate-200/80 outline-none ring-0 transition focus:border-[#FFBF00]/70 focus:bg-[#FFF7DA]"
                    placeholder="jane@example.com"
                  />
                </div>
              </div>
            </div>

            <button
              onClick={startCheckout}
              disabled={checkingOut || cart.length === 0}
              className="mt-4 inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-gradient-to-r from-[#FFBF00] via-[#FFE642] to-[#FF7900] px-4 py-2.5 text-xs font-semibold text-slate-900 shadow-lg shadow-orange-300/70 transition hover:brightness-110 disabled:cursor-not-allowed disabled:from-slate-300 disabled:via-slate-300 disabled:to-slate-400 disabled:text-slate-500 disabled:shadow-none"
            >
              {checkingOut ? 'Preparing checkout...' : 'Start USDC checkout'}
            </button>

            {orderId && (
              <div className="mt-5 space-y-2 rounded-2xl border border-[#FFBF00] bg-[#FFF7DA] p-3 text-xs text-slate-900">
                <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-[#B86400]">
                  Payment details
                </div>
                <div className="grid gap-1.5">
                  <div className="flex items-center justify-between">
                    <span>Order</span>
                    <span className="font-mono text-[11px] text-slate-900/80">
                      {orderId.slice(0, 10)}…
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span>Send</span>
                    <span className="font-semibold">
                      {amountUsdc?.toFixed(2)} USDC
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span>Network</span>
                    <span>{network}</span>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <span>Deposit address</span>
                    <div className="flex items-center gap-2">
                      <span className="flex-1 break-all rounded-xl bg-white/70 px-2 py-1 text-[11px] font-mono text-slate-900">
                        {depositAddress}
                      </span>
                      <button
                        type="button"
                        onClick={async () => {
                          if (!depositAddress) return
                          try {
                            if (navigator.clipboard?.writeText) {
                              await navigator.clipboard.writeText(depositAddress)
                            } else {
                              const textarea = document.createElement('textarea')
                              textarea.value = depositAddress
                              textarea.style.position = 'fixed'
                              textarea.style.opacity = '0'
                              document.body.appendChild(textarea)
                              textarea.focus()
                              textarea.select()
                              document.execCommand('copy')
                              document.body.removeChild(textarea)
                            }
                            setCopiedAddress(true)
                            setTimeout(() => setCopiedAddress(false), 2000)
                          } catch {
                            // ignore copy failures
                          }
                        }}
                        className="inline-flex items-center gap-1 rounded-full border border-[#FFBF00]/70 bg-white/90 px-2.5 py-1 text-[10px] font-medium text-[#B86400] shadow-sm shadow-orange-200/70 transition hover:border-[#FF7900] hover:bg-[#FFF7DA]"
                      >
                        {copiedAddress ? (
                          <>
                            <FiCheck className="text-xs" />
                            Copied
                          </>
                        ) : (
                          <>
                            <FiCopy className="text-xs" />
                            Copy
                          </>
                        )}
                      </button>
                    </div>
                  </div>
                  <div className="flex items-center justify-between pt-1">
                    <span>Status</span>
                    <span className="rounded-full bg-[#FFBF00]/20 px-2 py-0.5 text-[11px] font-medium uppercase tracking-[0.16em] text-[#B86400]">
                      {orderStatus}
                    </span>
                  </div>
                  {amountCop && amountCop > 0 && (
                    <div className="flex items-center justify-between">
                      <span>Converted to COP</span>
                      <span className="font-semibold tabular-nums">
                        ${amountCop.toLocaleString('es-CO')} COP
                      </span>
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </>
  )
}

function LoginPage({ onLogin }: { onLogin: (auth: AuthState) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()
  const location = useLocation() as any

  const from = location.state?.from ?? '/'

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    try {
      setLoading(true)
      const res = await axios.post(`${API_BASE}/api/login`, { username, password })
      const data = res.data as { token: string; role: Role }
      onLogin({ token: data.token, role: data.role })
      if (data.role === 'admin') {
        navigate('/admin', { replace: true })
      } else {
        navigate(from === '/login' || from === '/admin' ? '/' : from, {
          replace: true,
        })
      }
    } catch {
      setError('Invalid credentials. Try guest/guest or admin/admin.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-md flex-col gap-4">
      <div className="rounded-3xl border border-white/10 bg-slate-900/70 p-5 shadow-2xl shadow-black/60 backdrop-blur-2xl">
        <h1 className="text-lg font-semibold tracking-tight text-slate-50">
          Sign in
        </h1>
        <p className="mt-1 text-xs text-slate-300/85">
          Use <span className="font-semibold">guest/guest</span> to simulate a
          customer, or <span className="font-semibold">admin/admin</span> for the
          merchant view.
        </p>
        <form onSubmit={submit} className="mt-4 space-y-3 text-xs">
          <div className="space-y-1.5">
            <label className="text-[11px] font-medium text-slate-300/90">
              Username
            </label>
            <input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="h-8 w-full rounded-xl border border-slate-200 bg-white px-3 text-xs text-slate-900 shadow-inner shadow-slate-200/80 outline-none ring-0 transition focus:border-[#FFBF00]/70 focus:bg-[#FFF7DA]"
              placeholder="guest or admin"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-[11px] font-medium text-slate-300/90">
              Password
            </label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="h-8 w-full rounded-xl border border-slate-200 bg-white px-3 text-xs text-slate-900 shadow-inner shadow-slate-200/80 outline-none ring-0 transition focus:border-[#FFBF00]/70 focus:bg-[#FFF7DA]"
              placeholder="••••••"
            />
          </div>
          {error && (
            <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-[11px] text-rose-100">
              {error}
            </div>
          )}
          <button
            type="submit"
            disabled={loading}
            className="mt-2 inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-gradient-to-r from-[#FFBF00] via-[#FFE642] to-[#FF7900] px-4 py-2 text-xs font-semibold text-slate-900 shadow-lg shadow-orange-300/70 transition hover:brightness-110 disabled:cursor-not-allowed disabled:from-slate-300 disabled:via-slate-300 disabled:to-slate-400 disabled:text-slate-500 disabled:shadow-none"
          >
            {loading ? 'Signing in…' : 'Continue'}
          </button>
        </form>
      </div>
    </div>
  )
}

function AdminDashboard({ auth }: { auth: AuthState }) {
  const [orders, setOrders] = useState<Order[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeOrder, setActiveOrder] = useState<Order | null>(null)

  useEffect(() => {
    const fetchOrders = async () => {
      try {
        setLoading(true)
        const res = await axios.get<Order[]>(`${API_BASE}/api/admin/orders`, {
          headers: auth.token
            ? {
                Authorization: `Bearer ${auth.token}`,
              }
            : undefined,
        })
        setOrders(res.data)
      } catch {
        setError('Unable to fetch orders.')
      } finally {
        setLoading(false)
      }
    }
    if (auth.role === 'admin') {
      fetchOrders()
      const interval = setInterval(fetchOrders, 5000)
      return () => clearInterval(interval)
    }
  }, [auth])

  if (auth.role !== 'admin') {
    return <Navigate to="/" replace />
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-tight text-slate-900 md:text-2xl">
          Merchant operations
        </h1>
        <p className="mt-1 text-xs text-slate-600">
          Live view of incoming USDC payments, FX conversion into COP, and settlement
          into your bank account.
        </p>
        <div className="h-px w-32 bg-gradient-to-r from-[#FFBF00] via-[#F2CF7E] to-[#FF7900]" />
      </div>

      <div className="space-y-3">
        {loading ? (
          <p className="text-xs text-slate-500">Loading recent orders…</p>
        ) : error ? (
          <p className="text-xs text-rose-600">{error}</p>
        ) : orders.length === 0 ? (
          <p className="text-xs text-slate-500">
            No orders yet. Complete a test checkout from the storefront.
          </p>
        ) : (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {orders.map((o) => (
              <div
                key={o.id}
                onClick={() => setActiveOrder(o)}
                className="flex cursor-pointer flex-col justify-between rounded-3xl border border-slate-200/80 bg-[linear-gradient(135deg,#ffffff_0%,#f5f5f5_40%,#e5e7eb_100%)] p-4 text-xs text-slate-800 shadow-[0_18px_55px_rgba(148,163,184,0.45)] backdrop-blur-2xl transition hover:-translate-y-0.5 hover:border-slate-300 hover:shadow-[0_24px_80px_rgba(100,116,139,0.6)]"
              >
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-slate-500">
                      {o.status.replace('_', ' ')}
                    </div>
                    <div className="mt-1 font-medium text-slate-900">
                      {o.customerName}
                    </div>
                    <div className="text-[11px] text-slate-500">{o.customerEmail}</div>
                  </div>
                  <div className="text-right">
                    <div className="text-[11px] text-slate-400">Order</div>
                    <div className="font-mono text-[11px] text-slate-700">
                      {o.id.slice(0, 10)}…
                    </div>
                  </div>
                </div>

                <div className="mt-3 grid grid-cols-2 gap-2 rounded-2xl bg-white/80 p-3 text-[11px] text-slate-700">
                  <div>
                    <div className="text-slate-400">USDC paid</div>
                    <div className="mt-0.5 text-sm font-semibold text-[#FF7900]">
                      {o.amountUsdc.toFixed(2)} USDC
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="text-slate-400">COP (est.)</div>
                    <div className="mt-0.5 text-sm font-semibold text-slate-900">
                      {o.amountCop > 0
                        ? `$${o.amountCop.toLocaleString('es-CO')}`
                        : 'Pending'}
                    </div>
                  </div>
                </div>

                <div className="mt-3 flex items-center justify-between text-[11px] text-slate-500">
                  <span>
                    Created{' '}
                    {new Date(o.createdAt).toLocaleTimeString('en-US', {
                      hour: '2-digit',
                      minute: '2-digit',
                    })}
                  </span>
                  <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-medium uppercase tracking-[0.16em] text-slate-700">
                    {o.status}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      {activeOrder && (
        <div
          className="fixed inset-0 z-40 flex items-center justify-center bg-slate-900/20 px-4 pb-10 pt-20 backdrop-blur-2xl"
          onClick={(e) => {
            if (e.target === e.currentTarget) setActiveOrder(null)
          }}
        >
          <div className="relative w-full max-w-2xl rounded-3xl border border-slate-200/80 bg-[linear-gradient(135deg,#ffffff_0%,#fff7da_40%,#f2cf7e_100%)] p-5 text-xs text-slate-800 shadow-[0_28px_90px_rgba(15,23,42,0.45)]">
            <button
              type="button"
              onClick={() => setActiveOrder(null)}
              className="absolute right-4 top-4 rounded-full border border-slate-200 bg-white/90 px-2 py-1 text-[11px] text-slate-600 shadow-sm hover:border-slate-400 hover:bg-white"
            >
              Close
            </button>
            <div className="grid gap-4 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)] md:items-start">
              <div className="space-y-2">
                <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-slate-500">
                  {activeOrder.status.replace('_', ' ')}
                </div>
                <div className="text-sm font-semibold text-slate-900">
                  {activeOrder.customerName}
                </div>
                <div className="text-[11px] text-slate-500">
                  {activeOrder.customerEmail}
                </div>
                <div className="mt-2 rounded-2xl bg-white/80 p-3 shadow-inner shadow-slate-200/70">
                  <div className="text-[11px] font-medium uppercase tracking-[0.16em] text-slate-400">
                    Line items
                  </div>
                  <ul className="mt-2 space-y-1.5">
                    {activeOrder.items.map((item) => (
                      <li key={item.productId} className="flex items-center justify-between">
                        <span className="text-[11px] text-slate-700">
                          {item.name}{' '}
                          <span className="text-slate-400">× {item.quantity}</span>
                        </span>
                        <span className="text-[11px] font-medium text-slate-800">
                          {(item.priceUsdc * item.quantity).toFixed(2)} USDC
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
              <div className="space-y-3 rounded-2xl bg-white/80 p-3 shadow-inner shadow-orange-200/60">
                <div>
                  <div className="text-[11px] text-slate-400">USDC paid</div>
                  <div className="mt-0.5 text-sm font-semibold text-[#FF7900]">
                    {activeOrder.amountUsdc.toFixed(2)} USDC
                  </div>
                </div>
                <div>
                  <div className="text-[11px] text-slate-400">COP (est.)</div>
                  <div className="mt-0.5 text-sm font-semibold text-slate-900">
                    {activeOrder.amountCop > 0
                      ? `$${activeOrder.amountCop.toLocaleString('es-CO')}`
                      : 'Pending'}
                  </div>
                </div>
                <div className="pt-1 text-[11px] text-slate-500">
                  Created{' '}
                  {new Date(activeOrder.createdAt).toLocaleString('en-US', {
                    hour: '2-digit',
                    minute: '2-digit',
                    month: 'short',
                    day: '2-digit',
                  })}
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function App() {
  const [auth, setAuth, logout] = useAuth()

  return (
    <Shell auth={auth} onLogout={logout}>
      <Routes>
        <Route path="/" element={<CatalogPage auth={auth} />} />
        <Route path="/login" element={<LoginPage onLogin={setAuth} />} />
        <Route path="/admin" element={<AdminDashboard auth={auth} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  )
}

export default App
