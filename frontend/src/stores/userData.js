import { useMarketStore } from './market'
import { useSubscriptionsStore } from './subscriptions'

// resetUserData clears every store that holds the signed-in user's data, so
// the next user on the same browser never sees it (#53). Called from
// authStore.clearSession on logout and on an expired session.
export function resetUserData() {
  useMarketStore().reset()
  useSubscriptionsStore().reset()
}
