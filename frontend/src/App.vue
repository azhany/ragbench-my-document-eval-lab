<script setup>
import { onMounted, ref } from 'vue'

const liveness = ref({ state: 'pending', data: null })
const readiness = ref({ state: 'pending', data: null })

async function fetchJson(path) {
  try {
    const response = await fetch(path)
    let data = null
    try {
      data = await response.json()
    } catch (parseError) {
      data = { error: `HTTP ${response.status} without a JSON body` }
    }
    return { state: response.ok ? 'ok' : 'error', data }
  } catch (error) {
    return { state: 'unreachable', data: { error: String(error) } }
  }
}

async function refresh() {
  liveness.value = { state: 'pending', data: null }
  readiness.value = { state: 'pending', data: null }
  liveness.value = await fetchJson('/healthz')
  readiness.value = await fetchJson('/readyz')
}

onMounted(refresh)
</script>

<template>
  <main>
    <header>
      <h1>RAGbench-MY</h1>
      <p>Document library evaluation lab — local stack status</p>
    </header>

    <section class="cards">
      <article class="card">
        <h2>API liveness — <code>GET /healthz</code></h2>
        <p class="state" :data-state="liveness.state">status: {{ liveness.state }}</p>
        <pre>{{ JSON.stringify(liveness.data, null, 2) }}</pre>
      </article>
      <article class="card">
        <h2>API readiness — <code>GET /readyz</code></h2>
        <p class="state" :data-state="readiness.state">status: {{ readiness.state }}</p>
        <pre>{{ JSON.stringify(readiness.data, null, 2) }}</pre>
      </article>
    </section>

    <button @click="refresh">Refresh status</button>

    <footer>
      <p>
        Airflow UI:
        <a href="http://localhost:9090" target="_blank" rel="noreferrer">http://localhost:9090</a>
        — API:
        <a href="http://localhost:8080" target="_blank" rel="noreferrer">http://localhost:8080</a>
      </p>
    </footer>
  </main>
</template>

<style>
:root {
  color-scheme: light dark;
}

body {
  margin: 0;
  font-family: system-ui, -apple-system, sans-serif;
  line-height: 1.5;
}

main {
  max-width: 60rem;
  margin: 0 auto;
  padding: 2rem;
}

h1 {
  margin-bottom: 0;
}

header p {
  margin-top: 0.25rem;
  opacity: 0.75;
}

.cards {
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  margin: 2rem 0;
}

.card {
  border: 1px solid rgba(128, 128, 128, 0.4);
  border-radius: 0.5rem;
  padding: 0 1.25rem 1.25rem;
}

.card h2 {
  font-size: 1rem;
}

.state[data-state='ok'] {
  color: #1a7f37;
}

.state[data-state='error'],
.state[data-state='unreachable'] {
  color: #b42318;
}

pre {
  background: rgba(128, 128, 128, 0.12);
  border-radius: 0.25rem;
  overflow-x: auto;
  padding: 0.75rem;
}

button {
  cursor: pointer;
}

footer {
  margin-top: 2rem;
  opacity: 0.75;
}
</style>
