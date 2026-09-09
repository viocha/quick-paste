import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { useEffect, useState } from "react"
import { FolderIcon, FolderOpenIcon, XIcon } from "lucide-react"
import { Events } from "@wailsio/runtime"
import { BrowserService, PhraseService, type PhraseNode } from "../bindings/quick-phrase"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ScrollArea } from "@/components/ui/scroll-area"
import { PhraseRow } from "./PhraseRow"

export function BrowserApp() {
  const tooltipDelay=useTooltipDelay()
  const tooltipTimeout=useTooltipTimeout()
  const [nodes,setNodes]=useState<PhraseNode[]>([])
  const [expanded,setExpanded]=useState<Set<string>>(()=>new Set())
  const [error,setError]=useState("")
  const [busy,setBusy]=useState(false)
  const [generation,setGeneration]=useState(0)
  useEffect(()=>{
    let active=true
    const refresh=()=>{setError("");setGeneration(n=>n+1);void PhraseService.ListNodes().then(value=>{if(active)setNodes(value ?? [])}).catch(e=>{if(active)setError(String(e))})}
    const cancel=Events.On("browser-refresh",refresh)
    const hidden=Events.On("browser-hidden",()=>setGeneration(n=>n+1))
    refresh()
    return ()=>{active=false;cancel();hidden()}
  },[])
  const insert=async(id:string)=>{if(busy)return;setBusy(true);setError("");try{await BrowserService.Insert(id)}catch(e){setError(String(e))}finally{setBusy(false)}}
  const tree=(parent="",depth=0)=>nodes.filter(n=>(n.parentId??"")===parent)
    .sort((a,b)=>a.type!==b.type ? a.type==="folder" ? -1:1 : a.name.localeCompare(b.name,"zh-CN",{numeric:true})||a.id.localeCompare(b.id))
    .map(node=>{
      const open=expanded.has(node.id)
      return <li key={node.id}>
        {node.type==="folder" ? <>
          <button className="flex h-7 w-full items-center gap-1.5 rounded-sm px-2 text-left text-sm hover:bg-accent" style={{paddingLeft:6+depth*14}} aria-expanded={open} onClick={()=>setExpanded(current=>{const next=new Set(current);if(open)next.delete(node.id);else next.add(node.id);return next})}>
            {open?<FolderOpenIcon className="size-4 shrink-0" />:<FolderIcon className="size-4 shrink-0" />}<span className="truncate">{node.name}</span>
          </button>
          {open && <ul>{tree(node.id,depth+1)}</ul>}
        </> : <PhraseRow externalDetailId={node.id} name={node.name} content={node.content} render={<button disabled={busy} onClick={()=>void insert(node.id)} style={{paddingLeft:6+depth*14}} className="flex min-h-7 w-full items-center gap-1.5 rounded-sm px-2 py-0.5 text-sm leading-4 hover:bg-accent disabled:opacity-50" />} />}
      </li>
    })
  return <TooltipProvider delay={tooltipDelay} timeout={tooltipTimeout}>
    <div className="flex h-svh select-none flex-col overflow-hidden border bg-popover text-popover-foreground">
      <header onPointerDown={event=>{if(event.button===0 && !(event.target as HTMLElement).closest("button")){event.preventDefault();void BrowserService.BeginDrag().catch(e=>setError(String(e)))}}} className="flex h-7 shrink-0 cursor-move items-center justify-between border-b pl-2 pr-0.5">
        <span className="text-sm font-medium">短语浏览</span>
        <button className="flex size-6 items-center justify-center rounded-md hover:bg-accent" aria-label="关闭短语浏览" onClick={()=>void BrowserService.Hide()}><XIcon className="size-4" /></button>
      </header>
      <ScrollArea onScrollCapture={()=>void BrowserService.DismissDetail().catch(()=>{})} className="min-h-0 flex-1"><div><ul key={generation} aria-label="短语目录" className="p-0.5">{tree()}</ul>{nodes.length===0 && <p className="p-3 text-center text-xs text-muted-foreground">暂无短语</p>}
      {error && <p role="alert" className="shrink-0 border-t p-2 text-xs text-destructive">{error}</p>}
      </div></ScrollArea>
    </div>
  </TooltipProvider>
}
