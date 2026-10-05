// 把 flag-icons 的 4x3 国旗 SVG 复制到 public/flags/4x3，供 <img src="/flags/..."> 按需加载。
// 只取 4x3 集合：整包 CSS 同时引用 1x1（方形）集合，打进产物会多出一倍体积。
// 由 package.json 的 prebuild 触发，因此 build.sh 与 CI 都无需各自额外处理。
import { mkdir, readdir, copyFile, rm } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const src = join(here, '..', 'node_modules', 'flag-icons', 'flags', '4x3')
const dest = join(here, '..', 'public', 'flags', '4x3')

if (!existsSync(src)) {
  console.warn('[copy-flags] 未找到 flag-icons（请先 npm install），跳过国旗资源复制')
  process.exit(0)
}

await rm(dest, { recursive: true, force: true })
await mkdir(dest, { recursive: true })
const files = (await readdir(src)).filter((name) => name.endsWith('.svg'))
await Promise.all(files.map((name) => copyFile(join(src, name), join(dest, name))))
console.log(`[copy-flags] 已复制 ${files.length} 个国旗 SVG → public/flags/4x3`)
