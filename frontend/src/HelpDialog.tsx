import type { ReactNode } from "react"
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"

function Key({ children }: { children: ReactNode }) {
  return <kbd className="inline-flex min-w-5 items-center justify-center rounded border bg-muted px-1.5 py-0.5 font-mono text-[11px] leading-none text-foreground shadow-xs">{children}</kbd>
}

export function HelpDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="flex h-[360px] max-h-[calc(100svh-2rem)] max-w-md flex-col">
      <DialogTitle className="shrink-0">帮助</DialogTitle>

      <ScrollArea className="min-h-0 flex-1">
        <div className="space-y-4 pr-3 text-sm">
        <section className="space-y-2">
          <h2 className="font-medium">剪贴板</h2>
          <div className="space-y-2 text-muted-foreground">
            <p><span className="text-foreground">中键</span>：纯文本粘贴</p>
            <p className="flex items-center gap-1.5"><Key>Shift</Key><span>+</span><Key>Enter</Key><span>：纯文本粘贴</span></p>
            <p><span className="text-foreground">文件</span>：仅保存路径</p>
          </div>
        </section>

        <section className="space-y-2 border-t pt-4">
          <h2 className="font-medium">快捷面板</h2>
          <div className="space-y-2 text-muted-foreground">
            <p><span className="text-foreground">固定</span>：切换窗口不关闭，插入当前光标</p>
            <p className="flex items-center gap-1.5"><Key>Esc</Key><span>：关闭</span></p>
          </div>
        </section>

        <section className="space-y-2 border-t pt-4">
          <h2 className="font-medium">自动补全</h2>
          <p className="text-muted-foreground"><span className="text-foreground">文件夹开启</span>：所有后代短语生效</p>
        </section>
        </div>
      </ScrollArea>
    </DialogContent>
  </Dialog>
}
