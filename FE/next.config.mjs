/** @type {import('next').NextConfig} */
const nextConfig = {};

// /api is handled by src/app/api/[...path]/route.ts. A Next rewrite would
// forward browser Authorization directly to main-be and bypass that boundary.
export default nextConfig;
