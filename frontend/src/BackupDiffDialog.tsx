import { useEffect, useMemo, useState } from "react"
import * as jsondiffpatch from "jsondiffpatch"
import { format as formatHtml } from "jsondiffpatch/formatters/html"
import "jsondiffpatch/formatters/styles/html.css"
import { ChevronsDownUp, ChevronsUpDown } from "lucide-react"
import type { BackupPreview } from "../bindings/quick-paste"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import "./BackupDiffDialog.css"

type DiffDirection = "local-to-remote" | "remote-to-local"

const diffPatcher=jsondiffpatch.create({
 objectHash:value=>typeof value==="object" && value!==null && "id" in value && typeof value.id==="string" ? value.id : JSON.stringify(value),
 arrays:{detectMove:true,includeValueOnMove:false},
})

type DiffSummary={added:number;modified:number;deleted:number}
function summarizeDelta(delta:unknown,summary:DiffSummary) {
 if(!delta)return
 if(Array.isArray(delta)) {
  if(delta.length===1)summary.added++
  else if(delta.length===2)summary.modified++
  else if(delta.length>=3 && delta[1]===0 && delta[2]===0)summary.deleted++
  return
 }
 if(typeof delta!=="object")return
 for(const [key,value] of Object.entries(delta as Record<string,unknown>))if(key!=="_t")summarizeDelta(value,summary)
}

export function BackupDiffDialog({preview,open,onOpenChange}: {preview:BackupPreview|null;open:boolean;onOpenChange:(value:boolean)=>void}) {
 const [direction,setDirection]=useState<DiffDirection>("local-to-remote")
 const [showUnchanged,setShowUnchanged]=useState(false)
 useEffect(()=>{if(open){setDirection("local-to-remote");setShowUnchanged(false)}},[open,preview?.token])
 const comparison=useMemo(()=>{
  if(!preview)return null
  const localToRemote=direction==="local-to-remote"
  const current=localToRemote ? (preview.remote ?? []) : (preview.local ?? [])
  const desired=localToRemote ? (preview.local ?? []) : (preview.remote ?? [])
  const delta=diffPatcher.diff(current,desired)
  const summary:DiffSummary={added:0,modified:0,deleted:0}
  summarizeDelta(delta,summary)
  return {
   html:delta ? formatHtml(delta,current) ?? "" : "",
   hasChanges:delta!==undefined,
   summary,
  }
 },[preview,direction])
 if(!preview || !comparison)return null
 return <Dialog open={open} onOpenChange={onOpenChange}>
  <DialogContent className="flex h-[680px] max-h-[calc(100svh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
   <DialogHeader className="flex shrink-0 flex-row items-center justify-between gap-4 border-b px-6 py-4 pr-12">
    <DialogTitle>差异</DialogTitle>
    <DialogDescription className="sr-only">本地与云端备份差异</DialogDescription>
    <ToggleGroup value={[direction]} multiple={false} variant="outline" size="sm" spacing={0} onValueChange={values=>{const value=values[0];if(value)setDirection(value as DiffDirection)}} aria-label="差异方向">
     <ToggleGroupItem value="local-to-remote">本地 → 云端</ToggleGroupItem>
     <ToggleGroupItem value="remote-to-local">云端 → 本地</ToggleGroupItem>
    </ToggleGroup>
   </DialogHeader>
    <div className="flex min-h-0 flex-1 flex-col gap-3 p-6">
     <div className="flex shrink-0 items-center divide-x divide-border text-xs"><span className="pr-3"><span className="text-muted-foreground">新增</span> <span className="font-medium tabular-nums">{comparison.summary.added}</span></span><span className="px-3"><span className="text-muted-foreground">修改</span> <span className="font-medium tabular-nums">{comparison.summary.modified}</span></span><span className="pl-3"><span className="text-muted-foreground">删除</span> <span className="font-medium tabular-nums">{comparison.summary.deleted}</span></span></div>
    {comparison.hasChanges ? <section className="min-h-0 flex flex-1 flex-col overflow-hidden rounded-lg border bg-background">
      <div className="flex h-9 shrink-0 items-center justify-between border-b px-3 text-xs">
      <span>backup.json</span>
       <Button type="button" variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={()=>setShowUnchanged(value=>!value)}>
       {showUnchanged ? <ChevronsDownUp data-icon="inline-start" /> : <ChevronsUpDown data-icon="inline-start" />}
       {showUnchanged ? "折叠未变化" : "展开未变化"}
      </Button>
     </div>
     <ScrollArea className="min-h-0 flex-1">
      <div className={`backup-diff-view p-3 ${showUnchanged ? "jsondiffpatch-unchanged-visible" : "jsondiffpatch-unchanged-hidden"}`} dangerouslySetInnerHTML={{__html:comparison.html}} />
     </ScrollArea>
    </section> : <div className="flex flex-1 items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">无差异</div>}
   </div>
  </DialogContent>
 </Dialog>
}
