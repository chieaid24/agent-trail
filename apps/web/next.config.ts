import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // self-contained server.js for the production image (deploy/docker/Dockerfile web target)
  output: "standalone",
  // floating dev-tools badge occludes the sidebar footer
  devIndicators: false,
};

export default nextConfig;
