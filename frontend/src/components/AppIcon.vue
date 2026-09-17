<script setup>
defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 18 },
})
</script>

<template>
  <svg
    class="app-icon"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="name === 'overview'">
      <rect x="3" y="3" width="7" height="7" rx="1" />
      <rect x="14" y="3" width="7" height="7" rx="1" />
      <rect x="3" y="14" width="7" height="7" rx="1" />
      <rect x="14" y="14" width="7" height="7" rx="1" />
    </template>
    <template v-else-if="name === 'library'">
      <path d="M3 6.5A2.5 2.5 0 0 1 5.5 4H10l2 2h6.5A2.5 2.5 0 0 1 21 8.5v8A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5z" />
      <path d="M3 9h18" />
    </template>
    <template v-else-if="name === 'chat'">
      <path d="M20 11.5a7.5 7.5 0 0 1-7.5 7.5 7.8 7.8 0 0 1-3.2-.7L4 20l1.7-4.4A7.4 7.4 0 0 1 5 11.5 7.5 7.5 0 0 1 12.5 4 7.5 7.5 0 0 1 20 11.5z" />
      <path d="M9 12h.01M12.5 12h.01M16 12h.01" />
    </template>
    <template v-else-if="name === 'evaluations'">
      <path d="M5 4h14v16H5z" />
      <path d="m8 9 1.5 1.5L12 8M8 14l1.5 1.5L12 13M14 9h2M14 14h2" />
    </template>
    <template v-else-if="name === 'experiments'">
      <path d="M9 3h6M10 3v5l-5.5 9.2A2.5 2.5 0 0 0 6.6 21h10.8a2.5 2.5 0 0 0 2.1-3.8L14 8V3" />
      <path d="M8 15h8M9.2 12h5.6" />
    </template>
    <template v-else-if="name === 'monitor'">
      <path d="M4 18V9M8 18v-5M12 18V6M16 18v-8M20 18v-3" />
    </template>
    <template v-else-if="name === 'pipeline'">
      <rect x="3" y="4" width="6" height="5" rx="1" />
      <rect x="15" y="15" width="6" height="5" rx="1" />
      <path d="M9 6.5h3a3 3 0 0 1 3 3V17.5M15 17.5h-3a3 3 0 0 1-3-3V9.5" />
    </template>
    <template v-else-if="name === 'models'">
      <path d="m12 3 8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="m4.5 7.8 7.5 4.3 7.5-4.3M12 12.1V21" />
    </template>
    <template v-else-if="name === 'settings'">
      <path d="M4 6h16M4 12h16M4 18h16" />
      <circle cx="9" cy="6" r="2" fill="currentColor" stroke="none" />
      <circle cx="15" cy="12" r="2" fill="currentColor" stroke="none" />
      <circle cx="11" cy="18" r="2" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'upload'">
      <path d="M12 16V4M8 8l4-4 4 4M5 15v4h14v-4" />
    </template>
    <template v-else-if="name === 'refresh'">
      <path d="M20 11a8 8 0 0 0-14.7-4L3 10M3 5v5h5M4 13a8 8 0 0 0 14.7 4L21 14m0 5v-5h-5" />
    </template>
    <template v-else-if="name === 'check'">
      <circle cx="12" cy="12" r="9" />
      <path d="m8 12 2.5 2.5L16 9" />
    </template>
    <template v-else-if="name === 'alert'">
      <path d="m12 3 9 17H3z" />
      <path d="M12 9v4M12 16h.01" />
    </template>
    <template v-else>
      <circle cx="12" cy="12" r="8" />
    </template>
  </svg>
</template>
