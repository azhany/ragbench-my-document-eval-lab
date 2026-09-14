<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import ConfigList from '../components/ConfigList.vue'

// Health-check state is component-local: nothing else needs it, so no
// shared store is justified (see RB-04 state-locality rule).
const checks = ref([
  { name: 'API liveness', path: '/healthz', state: 'pending', detail: null },
  { name: 'API readiness', path: '/readyz', state: 'pending', detail: null },
])

async function refresh() {
  for (const check of checks.value) {
    check.state = 'pending'
    check.detail = null
  }
  await Promise.all(checks.value.map(refreshCheck))
}

async function refreshCheck(check) {
  try {
    const payload = await api.get(check.path)
    check.state = 'ok'
    check.detail = payload
  } catch (err) {
    // A 503 from /readyz is a meaningful health result, not a client bug:
    // render the payload instead of a generic failure.
    if (err.kind === 'http' && err.payload) {
      check.state = 'error'
      check.detail = err.payload
    } else {
      check.state = 'unreachable'
      check.detail = { message: err.message }
    }
  }
}

onMounted(refresh)
</script>

<template>
  <section>
    <div class="section-head">
      <h2>Stack status</h2>
      <button type="button" @click="refresh">Refresh status</button>
    </div>
    <div class="cards">
      <article v-for="check in checks" :key="check.path" class="card">
        <h3>{{ check.name }} — <code>{{ check.path }}</code></h3>
        <p class="state" :data-state="check.state">
          {{ check.state }}
        </p>
        <pre>{{ JSON.stringify(check.detail, null, 2) }}</pre>
      </article>
    </div>
  </section>

  <ConfigList />
</template>

<style scoped>
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
h2 {
  margin: 1.5rem 0 0.75rem;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  gap: 1rem;
}
.card {
  border: 1px solid var(--border);
  border-radius: 0.5rem;
  padding: 0.9rem 1rem;
  background: var(--surface);
}
.card h3 {
  margin: 0 0 0.5rem;
  font-size: 0.95rem;
  font-weight: 600;
}
.state {
  margin: 0 0 0.5rem;
  font-weight: 600;
}
.state[data-state='ok'] {
  color: var(--ok);
}
.state[data-state='error'],
.state[data-state='unreachable'] {
  color: var(--danger);
}
pre {
  background: var(--code-bg);
  border-radius: 0.4rem;
  padding: 0.6rem;
  overflow-x: auto;
  font-size: 0.8rem;
  margin: 0;
}
</style>
