<template>
  <header class="ui-page-heading" data-page-heading>
    <slot></slot>
  </header>
</template>

<style scoped>
.ui-page-heading { min-width: 0; }
.ui-page-heading :deep(h1) { overflow-wrap: anywhere; }
</style>
