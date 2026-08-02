import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './styles.css'
import { loadBrand } from './brand'

// Title + favicon before first paint of routes
loadBrand()

createApp(App).use(router).mount('#app')
