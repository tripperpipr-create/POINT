import fs from 'node:fs'

const stagedPath = process.argv[2]
const nativePath = process.argv[3]
const p = JSON.parse(fs.readFileSync(stagedPath, 'utf8'))
const old = JSON.parse(fs.readFileSync(nativePath, 'utf8'))
p.displayName = old.displayName
p.description = old.description
p.activationEvents = [...new Set(p.activationEvents || [])].filter(x => x !== 'onStartupFinished')
p.contributes.configurationDefaults = old.contributes.configurationDefaults
delete p.devDependencies
p.scripts = { check: p.scripts?.check }
fs.writeFileSync(stagedPath, JSON.stringify(p, null, 2) + '\n')
