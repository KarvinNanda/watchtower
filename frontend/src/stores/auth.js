import { defineStore } from 'pinia'
import { ref } from 'vue'

import router from '@/router'
import * as authApi from '@/api/auth'

// The JWT itself is never held here (or anywhere in JS) — it lives only in
// the httpOnly watchtower_token cookie the backend sets on login and reads
// back on every request. isAuthenticated instead reflects whether the
// backend has confirmed a valid session for us, via /auth/me (see
// checkAuth) or a fresh login.
export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const isAuthenticated = ref(false)
  const loading = ref(false)

  function clearSession() {
    user.value = null
    isAuthenticated.value = false
  }

  async function login(email, password) {
    loading.value = true
    try {
      await authApi.login(email, password)
      isAuthenticated.value = true
      await fetchProfile()
    } finally {
      loading.value = false
    }
  }

  async function register(email, password) {
    loading.value = true
    try {
      await authApi.register(email, password)
    } finally {
      loading.value = false
    }
  }

  async function logout() {
    try {
      await authApi.logout()
    } finally {
      clearSession()
      router.push({ name: 'login' })
    }
  }

  // fetchProfile loads the full user_profiles row (devices, os_list,
  // expertise_level, etc.) from /user/profile — used after login and by
  // views (ProfileView, AppLayout) that need more than the basic identity
  // fields /auth/me returns.
  async function fetchProfile() {
    const res = await authApi.getProfile()
    user.value = res.data
  }

  // checkAuth restores session state from the httpOnly cookie after a page
  // refresh (Pinia state is reset on every reload, but the cookie
  // persists) — called from the router's navigation guard before any
  // route that requires auth is entered. It's a lightweight probe against
  // /auth/me (not the fuller /user/profile fetchProfile uses), so a 401
  // here just means "not logged in," not an error to surface; views that
  // need the full profile still call fetchProfile() themselves.
  async function checkAuth() {
    try {
      const res = await authApi.me()
      user.value = res.user
      isAuthenticated.value = true
    } catch {
      clearSession()
    }
  }

  return {
    user,
    isAuthenticated,
    loading,
    login,
    register,
    logout,
    fetchProfile,
    checkAuth,
    clearSession,
  }
})
