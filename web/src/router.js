import { createRouter, createWebHistory } from 'vue-router'
import { getRole, getToken } from './api'
import Login from './views/Login.vue'
import AdminLayout from './views/AdminLayout.vue'
import Dashboard from './views/Dashboard.vue'
import Nodes from './views/Nodes.vue'
import Exits from './views/Exits.vue'
import Users from './views/Users.vue'
import Routes from './views/Routes.vue'
import Settings from './views/Settings.vue'
import Portal from './views/Portal.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    {
      path: '/',
      component: AdminLayout,
      meta: { auth: true, role: 'admin' },
      children: [
        { path: '', name: 'dashboard', component: Dashboard },
        { path: 'nodes', name: 'nodes', component: Nodes },
        { path: 'exits', name: 'exits', component: Exits },
        { path: 'users', name: 'users', component: Users },
        { path: 'routes', name: 'routes', component: Routes },
        { path: 'settings', name: 'settings', component: Settings },
      ],
    },
    { path: '/portal', component: Portal, meta: { auth: true, role: 'user' } },
  ],
})

router.beforeEach((to) => {
  if (!to.meta.auth) return true
  if (!getToken()) return '/login'
  const role = getRole()
  if (to.meta.role === 'admin' && role !== 'admin') return '/portal'
  if (to.meta.role === 'user' && role === 'admin') return '/'
  return true
})

export default router
