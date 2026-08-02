import { createRouter, createWebHistory } from 'vue-router'
import { getRole, getToken } from './api'
import Login from './views/Login.vue'
import AdminLayout from './views/AdminLayout.vue'
import Dashboard from './views/Dashboard.vue'
import Nodes from './views/Nodes.vue'
import Users from './views/Users.vue'
import Routes from './views/Routes.vue'
import Settings from './views/Settings.vue'
import Announcements from './views/Announcements.vue'
import Portal from './views/Portal.vue'
import UserInfo from './views/UserInfo.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    // Public read-only user info (token link from admin「更多」)
    { path: '/u/:token', name: 'user-info', component: UserInfo },
    {
      path: '/',
      component: AdminLayout,
      meta: { auth: true, role: 'admin' },
      children: [
        { path: '', name: 'dashboard', component: Dashboard },
        { path: 'nodes', name: 'nodes', component: Nodes },
        // legacy /exits → nodes tab
        { path: 'exits', redirect: { name: 'nodes', query: { tab: 'exit' } } },
        { path: 'users', name: 'users', component: Users },
        { path: 'routes', name: 'routes', component: Routes },
        { path: 'settings', name: 'settings', component: Settings },
        { path: 'announcements', name: 'announcements', component: Announcements },
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
