import { createRouter, createWebHistory } from 'vue-router'
import OverviewView from './views/OverviewView.vue'
import LibraryView from './views/LibraryView.vue'
import ChatView from './views/ChatView.vue'
import EvaluationsView from './views/EvaluationsView.vue'
import ExperimentsView from './views/ExperimentsView.vue'
import MonitorView from './views/MonitorView.vue'
import SettingsView from './views/SettingsView.vue'
// Keep the current workspace sitemap explicit; each destination owns its
// loading, empty, failure, or live-data state rather than relying on shell
// placeholders.
const routes = [
  {
    path: '/',
    name: 'overview',
    component: OverviewView,
    meta: { title: 'Overview' },
  },
  {
    path: '/library',
    name: 'library',
    component: LibraryView,
    meta: { title: 'Library', story: 'RB-05–RB-08 (Sprint 2)' },
  },
  {
    path: '/chat',
    name: 'chat',
    component: ChatView,
    meta: { title: 'Chat', story: 'RB-09–RB-12 (Sprint 3)' },
  },
  {
    path: '/evaluations',
    name: 'evaluations',
    component: EvaluationsView,
    meta: { title: 'Evaluations', story: 'RB-13–RB-16 (Sprint 4)' },
  },
  {
    path: '/experiments',
    name: 'experiments',
    component: ExperimentsView,
    meta: { title: 'Experiments', story: 'RB-17–RB-20 (Sprint 5)' },
  },
  {
    path: '/monitor',
    name: 'monitor',
    component: MonitorView,
    meta: { title: 'Monitor', story: 'RB-21–RB-24 (Sprint 6)' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsView,
    meta: { title: 'Settings', story: 'RB-33–RB-35 (Sprint 8)' },
  },
]

export function createAppRouter() {
  const router = createRouter({
    history: createWebHistory(),
    routes,
  })
  router.afterEach((to) => {
    document.title = `${to.meta.title} · RAGbench-MY`
  })
  return router
}
