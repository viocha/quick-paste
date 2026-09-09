import { Toaster } from "@/components/ui/toast"
import { TooltipDelayProvider } from "./TooltipDelay"
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'

if(location.hash === "#detail") document.documentElement.dataset.window="detail"

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <TooltipDelayProvider>{["#popup", "#detail"].includes(location.hash) ? <App /> : <Toaster><App /></Toaster>}</TooltipDelayProvider>
  </React.StrictMode>,
)
