import React from 'react'
import {createRoot} from 'react-dom/client'
import './styles/fonts'
import './style.css'
import App from './App'
import {applyTheme, DEFAULT_THEME} from './theme/theme'

applyTheme(DEFAULT_THEME)

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
