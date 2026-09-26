declare module '@novnc/novnc/lib/rfb' {
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { credentials?: Record<string, string>; shared?: boolean; repeaterID?: string; wsProtocols?: string[] })
    scaleViewport: boolean
    resizeSession: boolean
    focusOnClick: boolean
    viewOnly: boolean
    qualityLevel: number
    compressionLevel: number
    background: string
    disconnect(): void
    sendCtrlAltDel(): void
  }
}
