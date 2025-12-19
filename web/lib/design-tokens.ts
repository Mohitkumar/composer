/**
 * Design tokens for consistent styling across the application
 */

export const designTokens = {
  // Spacing
  spacing: {
    pageGap: 'gap-2',
    headerPadding: 'pb-3',
    contentPadding: 'pt-0',
  },

  // Typography
  typography: {
    pageTitle: 'text-xl',
    pageDescription: 'text-xs mt-0.5',
    tableHeader: 'h-9 text-xs font-medium',
    tableRow: 'h-11',
    tableCell: 'py-2 text-sm',
    tableCellSmall: 'py-2 text-xs',
  },

  // Buttons
  buttons: {
    small: 'h-8',
    icon: 'h-7 w-7 p-0',
    iconSize: 'h-3.5 w-3.5',
  },

  // Inputs
  inputs: {
    small: 'h-8 text-sm',
    searchIcon: 'h-3.5 w-3.5',
    searchPadding: 'pl-7',
  },

  // Badges
  badges: {
    small: 'text-xs',
  },

  // Pagination
  pagination: {
    text: 'text-xs',
    button: 'h-8',
  },
} as const;
