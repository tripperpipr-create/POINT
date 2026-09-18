import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

const outputDirectory = path.join(import.meta.dirname, 'resources');
fs.mkdirSync(outputDirectory, { recursive: true });

const crcTable = new Uint32Array(256);
for (let index = 0; index < 256; index += 1) {
  let value = index;
  for (let bit = 0; bit < 8; bit += 1) value = (value & 1) ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  crcTable[index] = value >>> 0;
}

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const value of buffer) crc = crcTable[(crc ^ value) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(type, contents) {
  const name = Buffer.from(type);
  const length = Buffer.alloc(4);
  length.writeUInt32BE(contents.length);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([name, contents])));
  return Buffer.concat([length, name, contents, checksum]);
}

function insideRoundedSquare(x, y, size, margin, radius) {
  const left = margin;
  const right = size - margin;
  const top = margin;
  const bottom = size - margin;
  if (x >= left + radius && x <= right - radius && y >= top && y <= bottom) return true;
  if (y >= top + radius && y <= bottom - radius && x >= left && x <= right) return true;
  const centerX = x < left + radius ? left + radius : right - radius;
  const centerY = y < top + radius ? top + radius : bottom - radius;
  return (x - centerX) ** 2 + (y - centerY) ** 2 <= radius ** 2;
}

function distanceToSegment(x, y, ax, ay, bx, by) {
  const dx = bx - ax;
  const dy = by - ay;
  const amount = Math.max(0, Math.min(1, ((x - ax) * dx + (y - ay) * dy) / (dx * dx + dy * dy)));
  return Math.hypot(x - (ax + amount * dx), y - (ay + amount * dy));
}

function raster(size) {
  const scale = 4;
  const high = size * scale;
  const source = new Uint8Array(high * high * 4);
  const margin = high * 0.055;
  const radius = high * 0.22;
  for (let y = 0; y < high; y += 1) {
    for (let x = 0; x < high; x += 1) {
      const offset = (y * high + x) * 4;
      if (!insideRoundedSquare(x + 0.5, y + 0.5, high, margin, radius)) continue;
      const blend = (x + y) / (high * 2);
      source[offset] = Math.round(19 - blend * 12);
      source[offset + 1] = Math.round(37 - blend * 20);
      source[offset + 2] = Math.round(53 - blend * 30);
      source[offset + 3] = 255;
      const edge = Math.min(x - margin, high - margin - x, y - margin, high - margin - y);
      if (edge < high * 0.024) {
        source[offset] = 45; source[offset + 1] = 159; source[offset + 2] = 148;
      }
      const nx = x / high;
      const ny = y / high;
      const pointStem = nx >= 0.27 && nx <= 0.37 && ny >= 0.22 && ny <= 0.78;
      const pointTop = nx >= 0.34 && nx <= 0.58 && ny >= 0.22 && ny <= 0.32;
      const pointMiddle = nx >= 0.34 && nx <= 0.58 && ny >= 0.44 && ny <= 0.54;
      const pointBowl = nx >= 0.56 && nx <= 0.66 && ny >= 0.29 && ny <= 0.47;
      if (pointStem || pointTop || pointMiddle || pointBowl) {
        source[offset] = 223; source[offset + 1] = 242; source[offset + 2] = 240; source[offset + 3] = 255;
      }
      const dotDistance = Math.hypot(nx - 0.69, ny - 0.70);
      if (dotDistance <= 0.085) {
        source[offset] = 90; source[offset + 1] = 220; source[offset + 2] = 205; source[offset + 3] = 255;
      }
    }
  }
  const pixels = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y += 1) {
    for (let x = 0; x < size; x += 1) {
      const totals = [0, 0, 0, 0];
      for (let sampleY = 0; sampleY < scale; sampleY += 1) {
        for (let sampleX = 0; sampleX < scale; sampleX += 1) {
          const sourceOffset = (((y * scale + sampleY) * high) + x * scale + sampleX) * 4;
          for (let channel = 0; channel < 4; channel += 1) totals[channel] += source[sourceOffset + channel];
        }
      }
      const destination = (y * size + x) * 4;
      for (let channel = 0; channel < 4; channel += 1) pixels[destination + channel] = Math.round(totals[channel] / (scale * scale));
    }
  }
  return pixels;
}

function png(size) {
  const pixels = raster(size);
  const rows = Buffer.alloc((size * 4 + 1) * size);
  for (let row = 0; row < size; row += 1) {
    const rowOffset = row * (size * 4 + 1);
    rows[rowOffset] = 0;
    pixels.copy(rows, rowOffset + 1, row * size * 4, (row + 1) * size * 4);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0); header.writeUInt32BE(size, 4);
  header[8] = 8; header[9] = 6;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(rows, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

const iconSizes = [16, 24, 32, 48, 64, 128, 256];
const iconImages = iconSizes.map(size => {
  const source = path.join(outputDirectory, `point-${size}.png`);
  if (!fs.existsSync(source)) throw new Error(`Missing Point icon asset: ${source}. Run prepare-point-icons.ps1 first.`);
  return { size, data: fs.readFileSync(source) };
});
const iconHeader = Buffer.alloc(6 + iconImages.length * 16);
iconHeader.writeUInt16LE(0, 0); iconHeader.writeUInt16LE(1, 2); iconHeader.writeUInt16LE(iconImages.length, 4);
let imageOffset = iconHeader.length;
for (let index = 0; index < iconImages.length; index += 1) {
  const { size, data } = iconImages[index];
  const offset = 6 + index * 16;
  iconHeader[offset] = size === 256 ? 0 : size;
  iconHeader[offset + 1] = size === 256 ? 0 : size;
  iconHeader.writeUInt16LE(1, offset + 4);
  iconHeader.writeUInt16LE(32, offset + 6);
  iconHeader.writeUInt32LE(data.length, offset + 8);
  iconHeader.writeUInt32LE(imageOffset, offset + 12);
  imageOffset += data.length;
}

fs.writeFileSync(path.join(outputDirectory, 'code.ico'), Buffer.concat([iconHeader, ...iconImages.map(image => image.data)]));
fs.copyFileSync(path.join(outputDirectory, 'point-70.png'), path.join(outputDirectory, 'code_70x70.png'));
fs.copyFileSync(path.join(outputDirectory, 'point-150.png'), path.join(outputDirectory, 'code_150x150.png'));
console.log(`Generated Point Windows icons in ${outputDirectory}`);
