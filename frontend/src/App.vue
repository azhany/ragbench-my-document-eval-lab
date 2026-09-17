<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppNav from './components/AppNav.vue'

const route = useRoute()
const today = new Intl.DateTimeFormat('en', {
  month: 'short', day: 'numeric', year: 'numeric',
}).format(new Date())
const pageTitle = computed(() => route.meta.title || 'Overview')
</script>

<template>
  <div class="app-shell">
    <AppNav />
    <div class="app-workspace">
      <header class="topbar">
        <div class="topbar-context">
          <span class="topbar-kicker">Workspace</span>
          <strong>{{ pageTitle }}</strong>
        </div>
        <div class="topbar-meta">
          <span>{{ today }}</span>
          <span class="system-status"><span class="online-dot"></span> All systems operational</span>
        </div>
      </header>
      <main class="page-content">
        <RouterView />
      </main>
      <footer class="site-footer">
        <span>RAGbench-MY · Document Library Eval Lab</span>
        <span><a href="http://localhost:9090" target="_blank" rel="noreferrer">Airflow UI</a><a href="http://localhost:8080" target="_blank" rel="noreferrer">API</a></span>
      </footer>
    </div>
  </div>
</template>
