import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import router from './router'
import i18n from './plugins/i18n'
import './assets/main.css'

// Naive UI components are imported individually per-file (tree-shakable)
// rather than registered globally — see src/plugins/naive.js for the
// shared theme/discrete-API setup those imports rely on.
const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)

app.mount('#app')
