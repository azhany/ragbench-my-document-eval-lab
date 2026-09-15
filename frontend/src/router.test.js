import { describe, expect, it } from 'vitest'
import { createAppRouter } from './router'

describe('router', () => {
  it('exposes all six navigation destinations', async () => {
    const router = createAppRouter()

    const destinations = [
      { path: '/', title: 'Overview' },
      { path: '/library', title: 'Library', story: 'RB-05–RB-08 (Sprint 2)' },
      { path: '/chat', title: 'Chat', story: 'RB-09–RB-12 (Sprint 3)' },
      { path: '/evaluations', title: 'Evaluations', story: 'RB-13–RB-16 (Sprint 4)' },
      { path: '/experiments', title: 'Experiments', story: 'RB-17–RB-20 (Sprint 5)' },
      { path: '/monitor', title: 'Monitor', story: 'RB-21–RB-24 (Sprint 6)' },
    ]

    for (const dest of destinations) {
      await router.push(dest.path)
      await router.isReady()
      expect(router.currentRoute.value.meta.title).toBe(dest.title)
      if (dest.story) {
        expect(router.currentRoute.value.meta.story).toBe(dest.story)
        expect(router.currentRoute.value.name).toBe(dest.path.slice(1))
      }
    }
  })

  it('routes unimplemented destinations to the explicit ComingSoon view', async () => {
    const router = createAppRouter()
    await router.push('/chat')
    await router.isReady()
    expect(router.currentRoute.value.matched[0].components.default.name).toBeUndefined()
    // The component identity check: ComingSoonView is shared, Overview is not.
    const chatComponent = router.currentRoute.value.matched[0].components.default
    await router.push('/')
    const overviewComponent = router.currentRoute.value.matched[0].components.default
    expect(chatComponent).not.toBe(overviewComponent)

    await router.push('/library')
    expect(router.currentRoute.value.matched[0].components.default).not.toBe(chatComponent)
  })
})
