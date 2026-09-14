<script setup>
import AppNav from './components/AppNav.vue'
</script>

<template>
  <div class="shell">
    <header class="site-header">
      <h1>RAGbench-MY</h1>
      <p class="subtitle">Document library evaluation lab</p>
      <AppNav />
    </header>
    <main>
      <RouterView />
    </main>
    <footer class="site-footer">
      <a href="http://localhost:9090" target="_blank" rel="noreferrer">Airflow UI</a>
      <a href="http://localhost:8080" target="_blank" rel="noreferrer">API</a>
    </footer>
  </div>
</template>

<style scoped>
.shell {
  max-width: 60rem;
  margin: 0 auto;
  padding: 1.5rem 1rem 3rem;
}
.site-header h1 {
  margin: 0;
  font-size: 1.6rem;
}
.subtitle {
  margin: 0.2rem 0 1rem;
  color: var(--muted);
}
.site-footer {
  margin-top: 2.5rem;
  display: flex;
  gap: 1.5rem;
  color: var(--muted);
  font-size: 0.9rem;
}
</style>
