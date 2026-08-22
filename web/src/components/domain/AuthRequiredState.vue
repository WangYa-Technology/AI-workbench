<script setup lang="ts">
import { LogIn, UserPlus } from 'lucide-vue-next'
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import UiButton from '../ui/UiButton.vue'

const props = withDefaults(defineProps<{
  title: string
  summary: string
  returnTo: string
  compact?: boolean
}>(), { compact: false })

const emit = defineEmits<{
  navigate: [mode: 'login' | 'register']
}>()

const { t } = useI18n()
const headingID = useId()
const safeReturnTo = computed(() => props.returnTo.startsWith('/') && !props.returnTo.startsWith('//') ? props.returnTo : '/')
const authTarget = (mode: 'login' | 'register') => ({ path: '/settings', query: { auth: mode, returnTo: safeReturnTo.value } })
</script>

<template>
  <section class="auth-required-state" :class="{ compact }" :aria-labelledby="headingID">
    <span class="auth-required-icon"><LogIn :size="20" /></span>
    <div class="auth-required-copy">
      <h2 :id="headingID">
        {{ title }}
      </h2>
      <p>{{ summary }}</p>
    </div>
    <div class="auth-required-actions">
      <UiButton as="RouterLink" class="command-button primary" variant="primary" :to="authTarget('login')" @click="emit('navigate', 'login')">
        <LogIn :size="17" />{{ t('account.signIn') }}
      </UiButton>
      <UiButton as="RouterLink" class="command-button secondary" variant="secondary" :to="authTarget('register')" @click="emit('navigate', 'register')">
        <UserPlus :size="17" />{{ t('account.createAccount') }}
      </UiButton>
    </div>
  </section>
</template>
