import type { NextConfig } from "next";

const apiOrigin = process.env.API_ORIGIN ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  // Strict mode mounts components twice in development. The run page opens
  // its event stream on mount, and aborting that stream cancels the run.
  reactStrictMode: false,
  // In production a reverse proxy routes /api to the Go server; this rewrite
  // serves the same purpose when the web app is reached directly.
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiOrigin}/api/:path*` }];
  },
  experimental: {
    // Event streams stay open for the whole compare run.
    proxyTimeout: 60 * 60 * 1000,
  },
};

export default nextConfig;
