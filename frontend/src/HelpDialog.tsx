import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Item, ItemContent, ItemDescription, ItemGroup, ItemTitle } from "@/components/ui/item"
import { Kbd } from "@/components/ui/kbd"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"

export function HelpDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="flex h-[480px] max-h-[calc(100svh-2rem)] flex-col sm:max-w-xl">
      <DialogHeader className="shrink-0"><DialogTitle>帮助</DialogTitle></DialogHeader>

      <ScrollArea className="min-h-0 flex-1">
        <div className="flex flex-col gap-4 pr-3">
        <section aria-labelledby="help-clipboard" className="flex flex-col gap-1">
          <h2 id="help-clipboard" className="px-2.5 text-sm font-medium">剪贴板</h2>
          <ItemGroup className="gap-0">
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal">中键</ItemTitle></ItemContent><ItemDescription>纯文本粘贴</ItemDescription></Item>
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal">文件</ItemTitle></ItemContent><ItemDescription>仅保存路径</ItemDescription></Item>
          </ItemGroup>
        </section>

        <Separator />
        <section aria-labelledby="help-browser" className="flex flex-col gap-1">
          <h2 id="help-browser" className="px-2.5 text-sm font-medium">快捷面板</h2>
          <ItemGroup className="gap-0">
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal">固定</ItemTitle></ItemContent><ItemDescription>切换窗口不关闭，插入当前光标</ItemDescription></Item>
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal"><Kbd>Ctrl+F</Kbd></ItemTitle></ItemContent><ItemDescription>聚焦搜索框</ItemDescription></Item>
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal"><Kbd>Ctrl+Tab</Kbd> / <Kbd>Shift+Tab</Kbd></ItemTitle></ItemContent><ItemDescription>面板显示时切换页签，无需聚焦</ItemDescription></Item>
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal"><Kbd>Esc</Kbd></ItemTitle></ItemContent><ItemDescription>关闭</ItemDescription></Item>
          </ItemGroup>
        </section>

        <Separator />
        <section aria-labelledby="help-completion" className="flex flex-col gap-1">
          <h2 id="help-completion" className="px-2.5 text-sm font-medium">自动补全</h2>
          <ItemGroup className="gap-0">
            <Item size="xs" className="py-0.5"><ItemContent><ItemTitle className="font-normal">文件夹开启</ItemTitle></ItemContent><ItemDescription>所有后代短语生效</ItemDescription></Item>
          </ItemGroup>
        </section>
        </div>
      </ScrollArea>
    </DialogContent>
  </Dialog>
}
