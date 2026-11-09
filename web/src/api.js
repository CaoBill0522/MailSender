const TOKEN_KEY = 'ms_token'

export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) } catch { return null }
}
export function setToken(t) {
  try { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY) } catch {}
}

async function request(method, url, body) {
  const res = await fetch('/admin/api' + url, {
    method,
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (getToken() || '') },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  let data = null
  try { data = await res.json() } catch {}
  if (res.status === 401 && url !== '/login') {
    setToken(null)
    window.location.href = '/login'
    throw new Error('登录已过期')
  }
  if (!res.ok) {
    const err = new Error(data?.message || `${res.status} ${res.statusText}`)
    err.code = data?.code
    throw err
  }
  return data
}

export const api = {
  get: (u) => request('GET', u),
  post: (u, b) => request('POST', u, b ?? {}),
  put: (u, b) => request('PUT', u, b ?? {}),
  del: (u) => request('DELETE', u),
}
