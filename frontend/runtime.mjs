// The outer handler also protects static and error responses, which bypass Start.
export function createHandler({ render, staticFiles }) {
  return async function fetch(request) {
    let response
    try {
      const pathname = new URL(request.url).pathname
      if (pathname === '/healthz' && ['GET', 'HEAD'].includes(request.method)) {
        response = new Response(request.method === 'HEAD' ? null : '{"ok":true}', {
          headers: { 'Content-Type': 'application/json; charset=utf-8' },
        })
      } else {
        response = await staticFiles(request, () => render(request))
      }
    } catch {
      // Do not return upstream details or credentials to clients.
      console.error('web request failed')
      response = new Response('Service unavailable', { status: 500 })
    }
    const headers = new Headers(response.headers)
    headers.set('X-Content-Type-Options', 'nosniff')
    headers.set('Referrer-Policy', 'strict-origin-when-cross-origin')
    headers.set('X-Frame-Options', 'DENY')
    headers.set('Cache-Control', 'no-store')
    headers.delete('Server')
    return new Response(response.body, { status: response.status, statusText: response.statusText, headers })
  }
}
