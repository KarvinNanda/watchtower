import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

import router from '@/router'
import * as authApi from '@/api/auth'

const TOKEN_KEY = 'watchtower-token'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem(TOKEN_KEY))
  const user = ref(null)
  const loading = ref(false)

  const isAuthenticated = computed(() => Boolean(token.value))

  function setSession(newToken) {
    token.value = newToken
    localStorage.setItem(TOKEN_KEY, newToken)
  }

  function clearSession() {
    token.value = null
    user.value = null
    localStorage.removeItem(TOKEN_KEY)
  }

  async function login(email, password) {
    loading.value = true
    try {
      const res = await authApi.login(email, password)
      setSession(res.token)
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

  function logout() {
    clearSession()
    router.push({ name: 'login' })
  }

  async function fetchProfile() {
    const res = await authApi.getProfile()
    user.value = res.data
  }

  return {
    token,
    user,
    loading,
    isAuthenticated,
    login,
    register,
    logout,
    fetchProfile,
    clearSession,
  }
})
