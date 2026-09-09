import { createContext, useContext, useEffect, useState, type ReactNode } from "react"
import { Events } from "@wailsio/runtime"
import { SettingsService } from "../bindings/quick-phrase"

const TooltipDelayContext=createContext(500)
const TooltipTimeoutContext=createContext(0)
export const useTooltipDelay=()=>useContext(TooltipDelayContext)
export const useTooltipTimeout=()=>useContext(TooltipTimeoutContext)

export function TooltipDelayProvider({children}:{children:ReactNode}) {
  const [delay,setDelay]=useState(500)
  const [timeout,setTimeoutValue]=useState(0)
  useEffect(()=>{
    let active=true,delayUpdated=false,timeoutUpdated=false
    const cancelDelay=Events.On("tooltip-delay-changed",event=>{delayUpdated=true;setDelay(event.data as number)})
    const cancelTimeout=Events.On("tooltip-timeout-changed",event=>{timeoutUpdated=true;setTimeoutValue(event.data as number)})
    void SettingsService.GetTooltipDelay().then(value=>{if(active&&!delayUpdated)setDelay(value)}).catch(()=>{})
    void SettingsService.GetTooltipTimeout().then(value=>{if(active&&!timeoutUpdated)setTimeoutValue(value)}).catch(()=>{})
    return ()=>{active=false;cancelDelay();cancelTimeout()}
  },[])
  return <TooltipDelayContext.Provider value={delay}><TooltipTimeoutContext.Provider value={timeout}>{children}</TooltipTimeoutContext.Provider></TooltipDelayContext.Provider>
}
