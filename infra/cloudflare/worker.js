// ==============================================================================
// SupportFlast Edge Worker - Cloudflare Workers & Static Assets
// Phục vụ giao diện web tĩnh từ supportflast_ui và chuyển tiếp API về Backend
// ==============================================================================

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // Chuyển tiếp các cuộc gọi API và WebDAV về máy chủ backend SupportFlast trên Render
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/webdav')) {
      const targetUrl = new URL(request.url);
      targetUrl.hostname = 'supportflastdev-io-vn.onrender.com';
      targetUrl.protocol = 'https:';
      targetUrl.port = '';

      const newHeaders = new Headers(request.headers);
      newHeaders.set('Host', 'supportflastdev-io-vn.onrender.com');
      newHeaders.set('X-Forwarded-Host', url.hostname);
      newHeaders.set('X-Forwarded-Proto', url.protocol.replace(':', ''));

      const proxyRequest = new Request(targetUrl.toString(), {
        method: request.method,
        headers: newHeaders,
        body: (request.method !== 'GET' && request.method !== 'HEAD') ? request.body : undefined,
        redirect: 'follow'
      });

      return fetch(proxyRequest);
    }

    // 2. Tự động chuyển hướng Canonical từ apex domain (supportflastdev.io.vn) sang www cho người dùng web
    if (url.hostname === 'supportflastdev.io.vn') {
      const canonicalUrl = new URL(request.url);
      canonicalUrl.hostname = 'www.supportflastdev.io.vn';
      return Response.redirect(canonicalUrl.toString(), 301);
    }

    // 3. Phục vụ tệp tĩnh (HTML, CSS, JS, ảnh) từ thư mục supportflast_ui
    if (env && env.ASSETS) {
      return env.ASSETS.fetch(request);
    }

    return new Response("SupportFlast Cloudflare Worker Ready", {
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      status: 200
    });
  }
};
