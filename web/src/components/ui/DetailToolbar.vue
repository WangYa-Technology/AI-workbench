<template>
  <div class="detail-toolbar" data-page-context>
    <slot></slot>
  </div>
</template>

<style scoped>
.detail-toolbar { min-height: 40px; display: flex; justify-content: space-between; align-items: center; gap: 16px; margin-bottom: 16px; }
.detail-toolbar :deep(> a.text-link) { min-height: 40px; margin: 0; gap: 8px; color: var(--text-secondary); font-size: 13px; }
.detail-toolbar :deep(> a.text-link > svg) { width: 17px; height: 17px; flex-shrink: 0; }
@media(max-width: 600px) { .detail-toolbar { flex-wrap: wrap; gap: 8px; } }
</style>
