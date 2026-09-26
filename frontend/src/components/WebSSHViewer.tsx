import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { RefreshCw, TerminalSquare, X } from 'lucide-react'
import { createWebSSHTicket } from '../services/api'

interface WebSSHViewerProps {
  containerName: string
  onClose: () => void
}

export default function WebSSHViewer({ containerName, onClose }: WebSSHViewerProps) {
  const terminalRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const resizeObserverRef = useRef<ResizeObserver | null>(null)
  const [status, setStatus] = useState<'connecting' | 'preparing' | 'connected' | 'disconnected' | 'error'>('connecting')
  const [errorMsg, setErrorMsg] = useState('')

  const buildWebSSHUrl = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const params = new URLSearchParams({
      container: containerName,
    })
    return `${protocol}//${window.location.host}/api/ssh?${params.toString()}`
  }

  const sendResize = () => {
    const ws = wsRef.current
    const term = termRef.current
    if (!ws || !term || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }

  const cleanup = () => {
    resizeObserverRef.current?.disconnect()
    resizeObserverRef.current = null

    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }

    if (termRef.current) {
      termRef.current.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }

  const connect = async () => {
    if (!terminalRef.current) return

    cleanup()
    setStatus('connecting')
    setErrorMsg('')

    const term = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: 'Consolas, Menlo, Monaco, monospace',
      fontSize: 13,
      theme: {
        background: '#050505',
        foreground: '#f3f4f6',
        cursor: '#ffffff',
        selectionBackground: '#374151',
      },
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(terminalRef.current)

    termRef.current = term
    fitRef.current = fitAddon

    const fitTerminal = () => {
      try {
        fitAddon.fit()
        sendResize()
      } catch {
        // The modal may report zero size during the first paint. Retry below.
      }
    }
    requestAnimationFrame(() => {
      fitTerminal()
      window.setTimeout(fitTerminal, 80)
      window.setTimeout(fitTerminal, 250)
    })

    let ticket = ''
    try {
      const response = await createWebSSHTicket(containerName)
      ticket = response.data.data?.ticket || ''
    } catch {
      setStatus('error')
      setErrorMsg('WebSSH ticket 创建失败，请重新登录后再试')
      return
    }
    if (!ticket) {
      setStatus('error')
      setErrorMsg('WebSSH ticket 为空，请重新登录后再试')
      return
    }

    const ws = new WebSocket(buildWebSSHUrl(), [`eyvescloud-ticket.${ticket}`])
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws

    term.writeln(`Connecting to ${containerName} as root...`)

    ws.onopen = () => {
      setStatus('preparing')
      term.writeln('\r\nWebSocket connected. Preparing SSH shell...')
      sendResize()
      term.focus()
    }

    ws.onmessage = async (event) => {
      setStatus('connected')
      if (event.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(event.data))
        return
      }

      if (event.data instanceof Blob) {
        const buffer = await event.data.arrayBuffer()
        term.write(new Uint8Array(buffer))
        return
      }

      term.write(String(event.data))
    }

    ws.onerror = () => {
      setStatus('error')
      setErrorMsg('WebSSH 连接失败，请确认容器已运行且 SSH 服务可用')
    }

    ws.onclose = () => {
      // Functional update only: avoids stale-closure reads of the outer `status`.
      setStatus((current) => (current === 'connected' ? 'disconnected' : current))
    }

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data))
      }
    })

    const observer = new ResizeObserver(() => {
      fitTerminal()
    })
    observer.observe(terminalRef.current)
    resizeObserverRef.current = observer
  }

  useEffect(() => {
    const timer = window.setTimeout(connect, 100)
    return () => {
      window.clearTimeout(timer)
      cleanup()
    }
  }, [containerName])

  return (
    <div className="bg-white border border-gray-200 rounded-lg overflow-hidden h-full flex flex-col">
      <div className="flex items-center justify-between px-4 py-2.5 border-b border-gray-200 bg-gray-50 shrink-0">
        <div className="flex items-center gap-2">
          <TerminalSquare className="w-4 h-4 text-gray-600" />
          <span className="text-sm font-medium text-black">WebSSH - {containerName}</span>
          {status === 'connected' && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-green-100 text-green-700">已连接</span>
          )}
          {status === 'connecting' && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-yellow-100 text-yellow-700">连接中...</span>
          )}
          {status === 'preparing' && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-yellow-100 text-yellow-700">SSH preparing...</span>
          )}
          {status === 'disconnected' && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-gray-100 text-gray-600">已断开</span>
          )}
          {status === 'error' && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-red-100 text-red-700">连接失败</span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={connect}
            className="p-1.5 hover:bg-gray-200 rounded text-gray-500 text-xs"
            title="重新连接"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={onClose}
            className="p-1.5 hover:bg-gray-200 rounded text-gray-500"
            title="关闭"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      </div>

      <div className="relative flex-1 bg-black min-h-[500px]">
        <div ref={terminalRef} className="absolute inset-0 p-2" />
        {status === 'error' && (
          <div className="absolute inset-x-0 bottom-0 border-t border-red-900 bg-red-950 px-4 py-2 text-sm text-red-100">
            {errorMsg}
          </div>
        )}
      </div>
    </div>
  )
}
