<script setup>
import { useRoute } from 'vue-router'

const route = useRoute()
</script>

<template>
  <section class="panel" data-state="unavailable">
    <h2>{{ route.meta.title }}</h2>
    <p>
      <strong>Not available yet.</strong> This destination is delivered by
      <code>{{ route.meta.story }}</code> and is intentionally left without
      sample data or fake success states.
    </p>
    <p>Implemented so far: Overview — stack status and saved RAG configurations.</p>
  </section>
</template>

<style scoped>
.panel {
  border: 1px dashed var(--border);
  border-radius: 0.5rem;
  padding: 1.25rem 1.5rem;
  background: var(--surface);
}
h2 {
  margin-top: 0;
}
code {
  background: var(--code-bg);
  padding: 0.1rem 0.35rem;
  border-radius: 0.3rem;
}
</style>
