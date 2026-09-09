import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { cloneElement, useEffect, useLayoutEffect, useRef, useState, type ReactElement } from "react"
import { FileTextIcon } from "lucide-react"
import { BrowserService } from "../bindings/quick-phrase"

let detailRequests=Promise.resolve()
let lastDetailClosedAt=0
function queueDetail(action:()=>PromiseLike<unknown>) {
  detailRequests=detailRequests.then(action).then(()=>{},()=>{})
}

export function PhraseRow({name, content, render, externalDetailId}: {name:string; content?:string; render:ReactElement; externalDetailId?:string}) {
  const tooltipDelay=useTooltipDelay()
  const tooltipTimeout=useTooltipTimeout()
  const summary=useRef<HTMLSpanElement>(null)
  const [truncated,setTruncated]=useState(false)
  const timer=useRef<ReturnType<typeof setTimeout>|null>(null)
  const clearTimer=()=>{if(timer.current!==null){clearTimeout(timer.current);timer.current=null}}

  useEffect(()=>()=>{clearTimer();if(externalDetailId)queueDetail(()=>BrowserService.HideDetail(externalDetailId))},[externalDetailId,tooltipDelay,tooltipTimeout])
  useLayoutEffect(()=>{
    const element=summary.current
    if(!element){setTruncated(false);return}
    const measure=()=>setTruncated(element.scrollWidth>element.clientWidth)
    measure()
    const observer=new ResizeObserver(measure);observer.observe(element)
    return ()=>observer.disconnect()
  },[content])
  const label=<>
      <FileTextIcon className="size-4 shrink-0 text-muted-foreground" />
      <span className="block min-w-0 flex-1 text-left">
        <span className="block truncate">{name}</span>
        {content && <span ref={summary} className="block truncate text-xs font-normal text-muted-foreground">{content.replace(/\s+/g," ")}</span>}
      </span>
  </>
  if(externalDetailId) return cloneElement(render,{
    onPointerEnter:(event:React.PointerEvent<HTMLButtonElement>)=>{
      clearTimer()
      const element=event.currentTarget
      // A native tooltip has its own hover lifecycle: never inherit the
      // component's instant-open state, even when re-entering the same row.
      queueDetail(()=>BrowserService.DismissDetail())
      const delay=tooltipTimeout>0 && Date.now()-lastDetailClosedAt<=tooltipTimeout ? 0 : tooltipDelay
      if(truncated)timer.current=setTimeout(()=>{
        timer.current=null
        const bounds=element.getBoundingClientRect()
        queueDetail(()=>BrowserService.ShowDetail(externalDetailId,Math.round(bounds.top),Math.round(bounds.height)))
      },delay)
    },
    onPointerLeave:()=>{clearTimer();lastDetailClosedAt=Date.now();queueDetail(()=>BrowserService.HideDetail(externalDetailId))},
    onPointerDown:()=>{clearTimer();lastDetailClosedAt=Date.now();queueDetail(()=>BrowserService.DismissDetail())},
    children:label,
  })
  return cloneElement(render,{children:label})
}
