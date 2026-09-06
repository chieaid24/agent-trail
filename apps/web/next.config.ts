import type { NextConfig } from "next";

const apiTarget = process.env.API_PROXY_TARGET ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // floating dev-tools badge occludes the sidebar footer
  devIndicators: false,
  async rewrites() {
    return [{ source: "/backend/:path*", destination: `${apiTarget}/:path*` }];
  },
};

export default nextConfig;
