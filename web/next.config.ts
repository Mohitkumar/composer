import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  /* config options here */
  webpack: (config, { isServer }) => {
    // Monaco Editor works only on client side
    if (!isServer) {
      config.resolve.fallback = {
        ...config.resolve.fallback,
        fs: false,
      };
    }
    return config;
  },
  // Add empty turbopack config to silence the warning
  // We're using webpack for Monaco Editor compatibility
  turbopack: {},
};

export default nextConfig;
