import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

/** Recompute product.json checksums so integrityService matches on-disk files. */
const appRoot = path.resolve(process.argv[2] || '');
if (!appRoot) {
  console.error('Usage: node refresh-product-checksums.mjs <resources/app>');
  process.exit(1);
}

const productPath = path.join(appRoot, 'product.json');
if (!fs.existsSync(productPath)) {
  console.error(`Missing product.json at ${productPath}`);
  process.exit(1);
}

const product = JSON.parse(fs.readFileSync(productPath, 'utf8'));
const checksums = product.checksums;
if (!checksums || typeof checksums !== 'object') {
  console.log(`No checksums in ${productPath}; nothing to refresh.`);
  process.exit(0);
}

function checksumFile(filePath) {
  const hash = crypto.createHash('sha256');
  hash.update(fs.readFileSync(filePath));
  return hash.digest('base64').replace(/=+$/, '');
}

let updated = 0;
let missing = 0;
for (const relative of Object.keys(checksums)) {
  const filePath = path.join(appRoot, 'out', ...relative.split('/'));
  if (!fs.existsSync(filePath)) {
    console.warn(`Missing checksum target: ${relative}`);
    missing += 1;
    continue;
  }
  const next = checksumFile(filePath);
  if (checksums[relative] !== next) {
    console.log(`Updated ${relative}`);
    console.log(`  was ${checksums[relative]}`);
    console.log(`  now ${next}`);
    checksums[relative] = next;
    updated += 1;
  }
}

if (updated > 0) {
  fs.writeFileSync(productPath, `${JSON.stringify(product, null, '\t')}\n`);
}
console.log(`Checksum refresh complete: updated=${updated} missing=${missing} file=${productPath}`);
