import fs from 'node:fs';
import path from 'node:path';

const nodeRoot = process.argv[2];
if (!nodeRoot) {
	throw new Error('Node runtime directory is required.');
}

const generatorPath = path.join(
	nodeRoot,
	'node_modules',
	'npm',
	'node_modules',
	'node-gyp',
	'gyp',
	'pylib',
	'gyp',
	'generator',
	'msvs.py',
);

const originalCondition = '        if spectre_mitigation:';
const patchedCondition = '        if spectre_mitigation and os.environ.get("LOCAL_AGENT_USE_STANDARD_CRT") != "1":';

const source = fs.readFileSync(generatorPath, 'utf8');
if (source.includes(patchedCondition)) {
	console.log('node-gyp fallback is already prepared.');
} else if (source.includes(originalCondition)) {
	fs.writeFileSync(generatorPath, source.replace(originalCondition, patchedCondition));
	console.log('Prepared node-gyp standard-CRT fallback.');
} else {
	throw new Error(`Unsupported node-gyp generator at ${generatorPath}`);
}
