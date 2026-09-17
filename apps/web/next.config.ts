import type { NextConfig } from "next";

const apiTarget = process.env.API_PROXY_TARGET ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // self-contained server.js for the production image (deploy/docker/Dockerfile web target)
  output: "standalone",
  // floating dev-tools badge occludes the sidebar footer
  devIndicators: false,
  async rewrites() {
    return [{ source: "/backend/:path*", destination: `${apiTarget}/:path*` }];
  },
};

export default nextConfig;
