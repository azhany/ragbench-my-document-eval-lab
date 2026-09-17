<script setup>
import { useRoute } from 'vue-router'
import AppIcon from './AppIcon.vue'

const route = useRoute()

const items = [
  { to: '/', label: 'Overview', icon: 'overview' },
  { to: '/library', label: 'Library', icon: 'library' },
  { to: '/chat', label: 'Chat', icon: 'chat' },
  { to: '/evaluations', label: 'Evaluations', icon: 'evaluations' },
  { to: '/experiments', label: 'Experiments', icon: 'experiments' },
  { to: '/monitor', label: 'Monitor', icon: 'monitor' },
]

const infrastructure = [
  { to: 'http://localhost:9090', label: 'Pipelines', caption: 'Airflow', icon: 'pipeline', external: true },
  { to: { name: 'settings', query: { tab: 'models' } }, label: 'Models', icon: 'models' },
  { to: { name: 'settings' }, label: 'Settings', icon: 'settings' },
]

function isActive(item) {
  if (item.external) return false
  const path = typeof item.to === 'string' ? item.to : item.to.path || '/settings'
  if (path === '/') return route.path === '/'
  if (route.path !== path) return false
  if (typeof item.to !== 'string' && item.to.query?.tab) return route.query.tab === item.to.query.tab
  if (path === '/settings') return route.query.tab !== 'models'
  return true
}
</script>

<template>
  <aside class="sidebar">
    <div class="brand">
      <div class="brand-mark">R</div>
      <div>
        <strong>RAGbench-MY</strong>
        <span>Document Library</span>
        <span>Eval Lab</span>
      </div>
    </div>

    <nav aria-label="Main navigation" class="nav-groups">
      <div class="nav-group">
        <p class="nav-label">Workspace</p>
        <RouterLink
          v-for="item in items"
          :key="item.to"
          :to="item.to"
          class="nav-link"
          :class="{ 'nav-link-active': isActive(item) }"
        >
          <AppIcon :name="item.icon" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </div>

      <div class="nav-group">
        <p class="nav-label">Infrastructure</p>
        <a
          v-for="item in infrastructure.filter((entry) => entry.external)"
          :key="item.label"
          class="nav-link"
          :href="item.to"
          target="_blank"
          rel="noreferrer"
        >
          <AppIcon :name="item.icon" />
          <span>{{ item.label }}</span>
          <small v-if="item.caption">{{ item.caption }}</small>
        </a>
        <RouterLink
          v-for="item in infrastructure.filter((entry) => !entry.external)"
          :key="item.label"
          :to="item.to"
          class="nav-link"
          :class="{ 'nav-link-active': isActive(item) }"
        >
          <AppIcon :name="item.icon" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </div>
    </nav>

    <div class="sidebar-footer">
      <div class="environment"><span class="online-dot"></span><span>Environment</span><strong>Development</strong></div>
      <span class="version">v0.1.0</span>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex: 0 0 244px;
  flex-direction: column;
  min-height: 100vh;
  padding: 26px 16px 18px;
  color: #c8d3e7;
  background: var(--sidebar);
}
.brand { display: flex; align-items: center; gap: 11px; padding: 0 10px 30px; color: #fff; }
.brand-mark { display: grid; width: 34px; height: 34px; place-items: center; border-radius: 10px; color: #fff; background: var(--accent); font-size: 18px; font-weight: 800; }
.brand strong, .brand span { display: block; }
.brand strong { font-size: 14px; letter-spacing: .01em; }
.brand span { margin-top: 2px; color: #8190aa; font-size: 10px; letter-spacing: .02em; }
.nav-groups { display: grid; gap: 28px; }
.nav-group { display: grid; gap: 4px; }
.nav-label { margin: 0 10px 7px; color: #687994; font-size: 10px; font-weight: 700; letter-spacing: .13em; text-transform: uppercase; }
.nav-link { display: flex; align-items: center; gap: 12px; min-height: 42px; padding: 0 11px; border-radius: 9px; color: #9ba9c0; font-size: 13px; font-weight: 600; text-decoration: none; transition: background .15s ease, color .15s ease; }
.nav-link .app-icon { flex: 0 0 auto; color: #8190aa; }
.nav-link small { margin-left: auto; color: #5e718f; font-size: 9px; font-weight: 500; }
.nav-link:hover, .nav-link-active { color: #fff; background: #183567; }
.nav-link-active .app-icon, .nav-link:hover .app-icon { color: #69a9ff; }
.sidebar-footer { display: flex; align-items: end; justify-content: space-between; gap: 8px; margin-top: auto; padding: 18px 10px 0; border-top: 1px solid #203452; color: #8090a9; font-size: 10px; }
.environment { display: grid; grid-template-columns: auto 1fr; gap: 2px 7px; align-items: center; }
.environment strong { grid-column: 2; color: #cbd5e6; font-size: 11px; font-weight: 600; }
.online-dot { width: 7px; height: 7px; border-radius: 50%; background: #41c891; box-shadow: 0 0 0 3px rgba(65, 200, 145, .12); }
.version { color: #60718c; }
@media (max-width: 760px) {
  .sidebar { position: static; flex-basis: auto; min-height: auto; padding: 14px; }
  .brand { padding-bottom: 16px; }
  .nav-groups { grid-template-columns: 1fr 1fr; gap: 14px; }
  .sidebar-footer { margin-top: 16px; }
}
</style>
