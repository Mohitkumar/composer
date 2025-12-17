# Composer Web UI

A modern Next.js-based web interface for the Composer workflow management platform.

## Features

- 🔐 **Authentication**: Login page with username/password
- 📊 **Dashboard**: Overview of workflows and executions with metrics
- 🔄 **Workflow Management**: 
  - List all workflows with pagination
  - Create new workflows with visual editor
  - Edit existing workflows
  - Delete workflows
  - Execute workflows
- 🎨 **Visual Workflow Editor**: ReactFlow-based editor similar to n8n for designing workflows
- 📈 **Executions**: View and manage workflow executions
- ⚙️ **Settings**: Configure API endpoints and preferences

## Tech Stack

- **Next.js 16** - React framework with App Router
- **TypeScript** - Type safety
- **shadcn/ui** - Beautiful UI components
- **ReactFlow** - Workflow visualization and editing
- **TanStack Query** - Data fetching and caching
- **Zustand** - State management
- **Tailwind CSS** - Styling

## Getting Started

### Prerequisites

- Node.js 18+ and npm
- Backend API running (default: http://localhost:8080)

### Installation

1. Install dependencies:
```bash
npm install
```

2. Create a `.env.local` file:
```bash
NEXT_PUBLIC_API_URL=http://localhost:8080
```

3. Run the development server:
```bash
npm run dev
```

4. Open [http://localhost:3000](http://localhost:3000) in your browser

### Login

Use any username and password to login (mock authentication). In production, replace with real authentication.

## Project Structure

```
web/
├── app/                    # Next.js app directory
│   ├── (dashboard)/       # Protected dashboard routes
│   │   ├── page.tsx       # Home page
│   │   ├── workflows/     # Workflow pages
│   │   ├── executions/    # Execution pages
│   │   ├── dashboard/    # Dashboard page
│   │   └── settings/      # Settings page
│   ├── login/             # Login page
│   └── layout.tsx         # Root layout
├── components/
│   ├── layout/            # Layout components (sidebar, etc.)
│   ├── workflow/          # Workflow editor component
│   └── ui/                # shadcn UI components
├── lib/
│   └── api-client.ts      # API client and types
└── store/
    └── auth-store.ts      # Authentication store
```

## API Integration

The frontend integrates with the backend API at `/api/v1`:

- `GET /workflows` - List workflows
- `POST /workflows` - Create workflow
- `GET /workflows/:id` - Get workflow
- `PUT /workflows/:id` - Update workflow
- `DELETE /workflows/:id` - Delete workflow
- `POST /workflows/:id/execute` - Execute workflow
- `GET /executions` - List executions
- `GET /executions/:id` - Get execution
- `POST /executions/:id/cancel` - Cancel execution

## Building for Production

```bash
npm run build
npm start
```

## Development Notes

- The workflow editor uses ReactFlow for visual workflow design
- Authentication is currently mocked - replace with real auth in production
- The API client automatically adds auth tokens from localStorage
- All API calls use TanStack Query for caching and state management
