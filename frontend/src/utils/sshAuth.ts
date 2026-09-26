export type SSHAuthMode = 'auto_password' | 'password' | 'key'
export type ReinstallSSHAuthMode = SSHAuthMode | 'keep'

const supportedKeyTypes = new Set([
  'ssh-ed25519',
  'ssh-rsa',
  'ecdsa-sha2-nistp256',
  'ecdsa-sha2-nistp384',
  'ecdsa-sha2-nistp521',
  'sk-ssh-ed25519@openssh.com',
  'sk-ecdsa-sha2-nistp256@openssh.com',
])

export function generateSSHPassword() {
  const letters = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
  const digits = '23456789'
  const symbols = '!@#$%*-_+='
  const all = letters + digits + symbols
  const pick = (chars: string) => chars[secureRandomInt(chars.length)]
  let password = pick(letters) + pick(digits)
  while (password.length < 16) password += pick(all)
  return secureShuffle(password.split('')).join('')
}

export function sshPasswordError(password: string) {
  if (password.length < 8 || password.length > 64) return '密码长度必须为 8-64 位'
  if (/\s/.test(password)) return '密码不能包含空白字符'
  if (!/[A-Za-z]/.test(password)) return '密码至少需要包含字母'
  if (!/\d/.test(password)) return '密码至少需要包含数字'
  return ''
}

export function sshPublicKeyError(publicKey: string) {
  const key = publicKey.trim()
  if (!key) return '请填写 SSH 公钥'
  if (key.length > 8192) return 'SSH 公钥长度不能超过 8192 字符'
  if (/[\r\n]/.test(key)) return 'SSH 公钥只能填写一行'
  const parts = key.split(/\s+/)
  if (parts.length < 2 || !supportedKeyTypes.has(parts[0])) return 'SSH 公钥格式不正确'
  return ''
}

function secureRandomInt(maxExclusive: number) {
  if (!Number.isSafeInteger(maxExclusive) || maxExclusive <= 0) {
    throw new Error('invalid random range')
  }
  const values = new Uint32Array(1)
  const maxUint32 = 0x100000000
  const limit = Math.floor(maxUint32 / maxExclusive) * maxExclusive
  let value = 0
  do {
    crypto.getRandomValues(values)
    value = values[0]
  } while (value >= limit)
  return value % maxExclusive
}

function secureShuffle<T>(items: T[]) {
  const next = [...items]
  for (let i = next.length - 1; i > 0; i--) {
    const j = secureRandomInt(i + 1)
    const value = next[i]
    next[i] = next[j]
    next[j] = value
  }
  return next
}
