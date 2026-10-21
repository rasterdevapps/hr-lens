import { Routes } from '@angular/router';
import { roleGuard, signedInGuard, signedOutGuard } from './core/auth.service';
import { ShellComponent } from './layout/shell/shell.component';

const timeTracker = () => import('./pages/dashboard/dashboard.component').then(m => m.DashboardComponent);
const monitoring = () => import('./pages/screenshots/screenshots.component').then(m => m.ScreenshotsComponent);
const activity = () => import('./pages/activity/activity.component').then(m => m.ActivityComponent);
const placeholder = () => import('./pages/placeholder/placeholder.component').then(m => m.PlaceholderComponent);

export const routes: Routes = [
  {
    path: 'login',
    loadComponent: () => import('./pages/login/login.component').then(m => m.LoginComponent),
    canActivate: [signedOutGuard],
    title: 'Sign in',
  },
  {
    path: '',
    component: ShellComponent,
    canActivate: [signedInGuard],
    canActivateChild: [roleGuard],
    children: [
      { path: '', redirectTo: 'dashboard', pathMatch: 'full' },
      { path: 'profile', loadComponent: () => import('./pages/profile/profile.component').then(m => m.ProfileComponent), title: 'My Profile' },

      // Time Tracker
      { path: 'dashboard', loadComponent: timeTracker, title: 'Dashboard', data: { tab: 'dashboard' } },
      { path: 'attendance', loadComponent: timeTracker, title: 'Attendance', data: { tab: 'attendance' } },
      { path: 'time-claim', loadComponent: timeTracker, title: 'Time Claim', data: { tab: 'time-claim' } },
      { path: 'heat-maps', loadComponent: timeTracker, title: 'Heat Maps', data: { tab: 'heat-maps' } },

      // Productivity
      {
        path: 'trends',
        loadComponent: () => import('./pages/productivity/trends.component').then(m => m.TrendsComponent),
        title: 'Trends',
      },
      {
        path: 'configure-apps',
        loadComponent: () => import('./pages/productivity/configure-apps.component').then(m => m.ConfigureAppsComponent),
        title: 'Configure Apps',
      },
      { path: 'productivity', redirectTo: 'trends' },

      // Activity
      { path: 'websites-visited', loadComponent: activity, title: 'Websites Visited', data: { tab: 'websites-visited' } },
      { path: 'applications-used', loadComponent: activity, title: 'Applications Used', data: { tab: 'applications-used' } },
      { path: 'by-categories', loadComponent: activity, title: 'By Categories', data: { tab: 'by-categories' } },
      { path: 'detailed-activity', loadComponent: activity, title: 'Detailed Activity', data: { tab: 'detailed-activity' } },
      { path: 'activity-monitoring', redirectTo: 'websites-visited' },

      // Monitoring
      { path: 'screenshots', loadComponent: monitoring, title: 'Screenshots', data: { tab: 'screenshots' } },
      { path: 'screen-recordings', loadComponent: monitoring, title: 'Screen Recordings', data: { tab: 'screen-recordings' } },
      { path: 'activity-levels', loadComponent: monitoring, title: 'Activity Levels', data: { tab: 'activity-levels' } },
      { path: 'screenshot-capture', redirectTo: 'screenshots' },
      { path: 'live-screen-monitoring', loadComponent: monitoring, title: 'Live Screen', data: { tab: 'live' } },

      // Not built yet
      { path: 'office-tv', loadComponent: () => import('./pages/office-tv/office-tv.component').then(m => m.OfficeTvComponent), title: 'Office TV' },
      { path: 'emails', loadComponent: () => import('./pages/emails/emails.component').then(m => m.EmailsComponent), title: 'Emails' },
      { path: 'desktops', loadComponent: () => import('./pages/desktops/desktops.component').then(m => m.DesktopsComponent), title: 'Desktops' },
      { path: 'users', loadComponent: () => import('./pages/users/users.component').then(m => m.UsersComponent), title: 'Users' },
      { path: 'teams', loadComponent: () => import('./pages/teams/teams.component').then(m => m.TeamsComponent), title: 'Teams' },
      { path: 'shifts', loadComponent: () => import('./pages/shifts/shifts.component').then(m => m.ShiftsComponent), title: 'Shifts' },
      { path: 'administration', redirectTo: 'users' },
    ],
  },
  { path: '**', redirectTo: 'dashboard' },
];
