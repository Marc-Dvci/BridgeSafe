import type { NextConfig } from "next";

const config: NextConfig = {
  // The demo runs locally against Coston2; there is no server-side data path,
  // so nothing here needs to reach the network at build time.
  reactStrictMode: true,
};

export default config;
