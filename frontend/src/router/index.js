import { createRouter, createWebHistory } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import AppLayout from '@/components/layout/AppLayout.vue'

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/views/RegisterView.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    component: AppLayout,
    meta: { requiresAuth: true },
    children: [
      { path: '', name: 'dashboard', component: () => import('@/views/DashboardView.vue') },
      { path: 'assets', name: 'assets', component: () => import('@/views/AssetSubView.vue') },
      { path: 'keywords', name: 'keywords', component: () => import('@/views/KeywordSubView.vue') },
      {
        path: 'notifications',
        name: 'notifications',
        component: () => import('@/views/NotificationsView.vue'),
      },
      { path: 'profile', name: 'profile', component: () => import('@/views/ProfileView.vue') },
      {
        path: 'telegram-setup',
        name: 'telegram-setup',
        component: () => import('@/views/TelegramSetupView.vue'),
      },
    ],
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// Pinia state resets on every hard page load/refresh, but the httpOnly
// auth cookie survives it — so isAuthenticated alone can't be trusted on
// first navigation after a reload. checkAuth() probes /auth/me to restore
// it from the cookie before this guard decides anything; skipped once a
// session has already been established in this tab (either by an earlier
// checkAuth or a fresh login), since /auth/me is a real network round trip.
router.beforeEach(async (to) => {
  const authStore = useAuthStore()

  if (!authStore.isAuthenticated) {
    await authStore.checkAuth()
  }

  if (to.meta.requiresAuth && !authStore.isAuthenticated) {
    return { name: 'login' }
  }
  if (to.meta.public && authStore.isAuthenticated) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
