import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  define: {
    // vue-i18n compiles messages at runtime. Without JIT mode it builds them
    // with `new Function`, which the nginx CSP (no 'unsafe-eval') blocks and
    // the production build renders a blank page. JIT compiles to an AST and
    // evaluates it without eval.
    __INTLIFY_JIT_COMPILATION__: true,
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
  },
})
