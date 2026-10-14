import { defineConfig } from '@playwright/test'
import production from './playwright.production.config'

const servers = Array.isArray(production.webServer) ? production.webServer : []
const bundle = process.env.DENOVA_PERFORMANCE_DIST_DIR

// Measure the shipped bundle against an isolated backend and data directory.
export default defineConfig({
  ...production,
  testMatch: 'performance/**/*.spec.ts',
  timeout: 180_000,
  use: { ...production.use, reducedMotion: 'reduce' },
  webServer: bundle ? servers.map((server, index) => index === servers.length - 1
    ? { ...server, command: `${server.command} --outDir "${bundle}"` }
    : server) : servers,
})
