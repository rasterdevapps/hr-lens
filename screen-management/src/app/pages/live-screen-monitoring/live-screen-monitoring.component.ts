import { Component, signal } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatButtonModule } from '@angular/material/button';
import { MatListModule } from '@angular/material/list';
import { MatChipsModule } from '@angular/material/chips';

interface Session { id: number; employee: string; status: 'Online' | 'Idle' | 'Offline'; app: string; }

@Component({
  selector: 'app-live-screen-monitoring',
  standalone: true,
  imports: [MatCardModule, MatIconModule, MatButtonModule, MatListModule, MatChipsModule],
  templateUrl: './live-screen-monitoring.component.html',
  styleUrl: './live-screen-monitoring.component.scss',
})
export class LiveScreenMonitoringComponent {
  sessions: Session[] = [
    { id: 1, employee: 'Alice Johnson', status: 'Online', app: 'VS Code' },
    { id: 2, employee: 'Bob Smith', status: 'Online', app: 'Chrome' },
    { id: 3, employee: 'Carol Lee', status: 'Idle', app: 'Slack' },
    { id: 4, employee: 'David Kim', status: 'Offline', app: '—' },
  ];
  selected = signal<Session>(this.sessions[0]);
  streaming = signal(false);

  select(s: Session) { this.selected.set(s); this.streaming.set(false); }
  toggleStream() { this.streaming.update(v => !v); }
}
