import { createRouter, createWebHistory } from 'vue-router'
import OverviewView from './views/OverviewView.vue'
import LibraryView from './views/LibraryView.vue'
import ChatView from './views/ChatView.vue'
import EvaluationsView from './views/EvaluationsView.vue'
import ExperimentsView from './views/ExperimentsView.vue'
import MonitorView from './views/MonitorView.vue'
// Destinations whose owning stories have not landed yet route to
// ComingSoonView, which states the owning stories explicitly and renders no
// sample data or fake metrics.
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
