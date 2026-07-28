declare module '@novnc/novnc' {
  export default class RFB extends EventTarget {
    constructor(
      target: HTMLElement,
      url: string,
      options?: {
        shared?: boolean
        credentials?: { username?: string; password?: string; target?: string }
        repeaterID?: string
        wsProtocols?: string[]
      },
    )
    viewOnly: boolean
    scaleViewport: boolean
    resizeSession: boolean
    focusOnClick: boolean
    background: string
    disconnect(): void
    focus(): void
    blur(): void
    sendKey(keysym: number, code: string | null, down?: boolean): void
    sendCtrlAltDel(): void
  }
}
