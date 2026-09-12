import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react"
import { Events } from "@wailsio/runtime"
import { BrowserService, type BrowserDetail } from "../bindings/quick-paste"
import { Tooltip, TooltipContent, TooltipProvider } from "@/components/ui/tooltip"
import { ScrollArea } from "@/components/ui/scroll-area"

export function DetailApp() {
  const tooltipDelay=useTooltipDelay()
  const tooltipTimeout=useTooltipTimeout()
  const [detail,setDetail]=useState<BrowserDetail>({token:0,content:"",side:"right",arrowY:16,scrollable:false,image:""})
  const [content,setContent]=useState<HTMLDivElement|null>(null)
  const [insets,setInsets]=useState(28)
  const anchor=useRef<HTMLDivElement>(null)
  useEffect(()=>{
    let active=true
    const update=(value:BrowserDetail)=>{if(active)setDetail(current=>value.token>=current.token ? value : current)}
    const cancel=Events.On("browser-detail",event=>update(event.data as BrowserDetail))
    const layout=Events.On("browser-detail-layout",event=>update(event.data as BrowserDetail))
    void BrowserService.GetDetail().then(update).catch(()=>{})
    return ()=>{active=false;cancel();layout()}
  },[])
  const previewText=detail.image ? "" : detail.content.replace(/[\r\n]+$/,"")
  useLayoutEffect(()=>{
    const element=content
    if(!element || (!previewText&&!detail.image))return
    element.closest('[data-slot="scroll-area-viewport"]')?.scrollTo(0,0)
    const resize=()=>{
      const popup=element.closest('[data-slot="tooltip-content"]')
      if(!popup)return
      const style=getComputedStyle(popup)
      const chrome=16+parseFloat(style.paddingTop)+parseFloat(style.paddingBottom)
      setInsets(chrome)
      // Read natural layout dimensions, never the previous scroll viewport.
      const natural=Math.max(element.scrollHeight,parseFloat(getComputedStyle(element).height)||0)
      void BrowserService.DetailReady(detail.token,Math.ceil(natural+chrome)).catch(()=>{})
    }
    const observer=new ResizeObserver(resize);observer.observe(element);resize()
    return ()=>observer.disconnect()
  },[detail.token,detail.image,previewText,content])
  const side=detail.side==="left" ? "left" : "right"
  return <TooltipProvider delay={tooltipDelay} timeout={tooltipTimeout}>
    <div ref={anchor} aria-hidden="true" style={{position:"absolute",top:8,[side==="right" ? "left":"right"]:4,width:0,height:32}} />
    <Tooltip open={!!detail.image||!!previewText}>
      <TooltipContent side={side} align="start" sideOffset={2}
        positionerProps={{anchor,collisionAvoidance:{side:"none",align:"none"}}}
        arrowStyle={{top:Math.max(6,(detail.arrowY||16)-8)}}
        className="block w-[calc(100vw-16px)] max-w-none">
        {detail.scrollable ? <ScrollArea className="w-full" scrollbarClassName="right-[-10px]! z-[60]" style={{"--detail-insets":`${insets}px`} as CSSProperties} viewportClassName="max-h-[calc(100svh-var(--detail-insets))]">
          <div ref={setContent}>{detail.image&&<img src={detail.image} alt="剪贴板图片预览" className="max-h-96 w-full object-contain"/>}{previewText&&<div className="whitespace-pre-wrap break-words">{previewText}</div>}</div>
        </ScrollArea> : <div ref={setContent}>{detail.image&&<img src={detail.image} alt="剪贴板图片预览" className="max-h-96 w-full object-contain"/>}{previewText&&<div className="whitespace-pre-wrap break-words">{previewText}</div>}</div>}
      </TooltipContent>
    </Tooltip>
  </TooltipProvider>
}
