import type { NextConfig } from "next";

// The browser reaches the control plane through this same-origin prefix and
// Next proxies it, so the API needs no CORS surface. API_PROXY_TARGET points
// at the API process; the default matches scripts/dev.sh.
const apiTarget = process.env.API_PROXY_TARGET ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // The floating dev-tools badge occludes the sidebar footer in audits.
  devIndicators: false,
  async rewrites() {
    return [{ source: "/backend/:path*", destination: `${apiTarget}/:path*` }];
  },
};

export default nextConfig;
