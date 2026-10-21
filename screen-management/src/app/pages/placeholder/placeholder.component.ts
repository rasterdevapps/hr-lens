import { Component, inject } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { MatIconModule } from '@angular/material/icon';

@Component({
  selector: 'app-placeholder',
  standalone: true,
  imports: [MatIconModule],
  template: `
    <div class="placeholder-box">
      <mat-icon>construction</mat-icon>
      <strong>{{ title }} is coming soon</strong>
      <span>This page is part of a later stage of HR Lens.</span>
    </div>
  `,
})
export class PlaceholderComponent {
  readonly title = inject(Title).getTitle();
}
