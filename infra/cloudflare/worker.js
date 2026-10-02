// ==============================================================================
// SupportFlast Edge Worker - Cloudflare Workers & Static Assets
// Phục vụ giao diện web tĩnh từ supportflast_ui và chuyển tiếp API về Backend
// ==============================================================================

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // Chuyển tiếp các cuộc gọi API về máy chủ backend SupportFlast
    if (url.pathname.startsWith('/api/')) {
      const targetUrl = new URL(request.url);
      targetUrl.hostname = 'supportflastdev.io.vn';
      targetUrl.protocol = 'https:';
      targetUrl.port = '';
      return fetch(new Request(targetUrl.toString(), request));
    }

    // Phục vụ tệp tĩnh (HTML, CSS, JS, ảnh) từ thư mục supportflast_ui
    if (env && env.ASSETS) {
      return env.ASSETS.fetch(request);
    }

    return new Response("SupportFlast Cloudflare Worker Ready", {
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      status: 200
    });
  }
};
