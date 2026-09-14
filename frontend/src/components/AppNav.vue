<script setup>
const items = [
  { to: '/', label: 'Overview' },
  { to: '/library', label: 'Library' },
  { to: '/chat', label: 'Chat' },
  { to: '/evaluations', label: 'Evaluations' },
  { to: '/experiments', label: 'Experiments' },
  { to: '/monitor', label: 'Monitor' },
]
</script>

<template>
  <nav aria-label="Main navigation">
    <RouterLink v-for="item in items" :key="item.to" :to="item.to">
      {{ item.label }}
    </RouterLink>
  </nav>
</template>

<style scoped>
nav {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  border-bottom: 1px solid var(--border);
  padding-bottom: 0.5rem;
}
a {
  padding: 0.35rem 0.75rem;
  border-radius: 0.4rem;
  text-decoration: none;
  color: var(--muted);
  font-weight: 500;
}
a:hover {
  color: var(--text);
  background: var(--surface-hover);
}
a.router-link-exact-active {
  color: var(--accent);
  background: var(--surface-hover);
}
</style>
