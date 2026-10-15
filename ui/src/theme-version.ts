import { useEffect, useState } from 'react'

/**
 * Bump when the host theme class / data-theme changes so MiniMap colors refresh.
 * Does not import Console theme events.
 */
export function useThemeVersion(): number {
  const [version, setVersion] = useState(0)
  useEffect(() => {
    const root = document.documentElement
    const bump = () => setVersion((v) => v + 1)
    const obs = new MutationObserver(bump)
    obs.observe(root, { attributes: true, attributeFilter: ['class', 'data-theme', 'data-color-mode'] })
    window.addEventListener('themechange', bump)
    return () => {
      obs.disconnect()
      window.removeEventListener('themechange', bump)
    }
  }, [])
  return version
}
