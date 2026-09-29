import { readFileSync, readdirSync } from 'node:fs'

// The manifest keeps CSS imports usable in a browser preview. The Code-OSS
// overlay receives one concatenated file so its existing asset path and
// cascade stay unchanged.
export function readPointWorkbenchCss() {
  const manifest = readFileSync(new URL('./resources/point-workbench.css', import.meta.url), 'utf8')
  const source = manifest.replace(/\/\*[\s\S]*?\*\//g, '').trim()
  const importRule = /@import\s+"\.\/point-workbench\/([0-9a-z-]+\.css)"\s*;/g
  const files = [...source.matchAll(importRule)].map(match => match[1])
  if (!files.length || source.replace(importRule, '').trim()) {
    throw new Error('point-workbench.css must contain only ordered local CSS imports')
  }
  const directory = new URL('./resources/point-workbench/', import.meta.url)
  const actual = readdirSync(directory).filter(name => name.endsWith('.css')).sort()
  if (files.length !== actual.length || files.some((name, index) => name !== actual[index])) {
    throw new Error('point-workbench.css imports must list every CSS part in filename order')
  }
  return files.map(name => readFileSync(new URL(`./resources/point-workbench/${name}`, import.meta.url), 'utf8')).join('')
}
