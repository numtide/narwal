#!/usr/bin/env nix-shell
#!nix-shell -i python3 -p "python3.withPackages (ps: [ ps.pandas ps.matplotlib ])"
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates

# Load data
df = pd.read_csv('/home/brian/Development/com/github/numtide/narwal/.data/daily-inventory-summary.csv')
df['day'] = pd.to_datetime(df['day'])

# Create figure with subplots
fig, axes = plt.subplots(3, 1, figsize=(14, 12))

# Plot 1: Total entries per day
ax1 = axes[0]
ax1.fill_between(df['day'], df['total_entries'], alpha=0.7, color='steelblue')
ax1.set_ylabel('Total Entries')
ax1.set_title('Daily Inventory Entries (All narinfo)')
ax1.xaxis.set_major_locator(mdates.YearLocator())
ax1.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax1.grid(True, alpha=0.3)

# Plot 2: Orphan entries per day
ax2 = axes[1]
ax2.fill_between(df['day'], df['orphan_entries'], alpha=0.7, color='coral')
ax2.set_ylabel('Orphan Entries')
ax2.set_title('Daily Orphan Entries (Not in Buildstepoutputs)')
ax2.xaxis.set_major_locator(mdates.YearLocator())
ax2.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax2.grid(True, alpha=0.3)

# Plot 3: Orphan percentage
ax3 = axes[2]
ax3.fill_between(df['day'], df['orphan_pct'], alpha=0.7, color='forestgreen')
ax3.set_ylabel('Orphan %')
ax3.set_xlabel('Date')
ax3.set_title('Daily Orphan Percentage')
ax3.xaxis.set_major_locator(mdates.YearLocator())
ax3.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax3.grid(True, alpha=0.3)

plt.tight_layout()
plt.savefig('/home/brian/Development/com/github/numtide/narwal/.data/inventory-daily.png', dpi=150)
print("Saved: inventory-daily.png")

# Monthly aggregation
df['month'] = df['day'].dt.to_period('M').dt.to_timestamp()
monthly = df.groupby('month').agg({
    'total_entries': 'sum',
    'orphan_entries': 'sum'
}).reset_index()
monthly['orphan_pct'] = monthly['orphan_entries'] * 100.0 / monthly['total_entries']

# Create monthly figure
fig2, axes2 = plt.subplots(3, 1, figsize=(14, 12))

# Plot 1: Monthly total entries
ax1 = axes2[0]
ax1.bar(monthly['month'], monthly['total_entries'], width=25, color='steelblue', alpha=0.8)
ax1.set_ylabel('Total Entries')
ax1.set_title('Monthly Inventory Entries (All narinfo)')
ax1.xaxis.set_major_locator(mdates.YearLocator())
ax1.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax1.grid(True, alpha=0.3, axis='y')

# Plot 2: Monthly orphan entries
ax2 = axes2[1]
ax2.bar(monthly['month'], monthly['orphan_entries'], width=25, color='coral', alpha=0.8)
ax2.set_ylabel('Orphan Entries')
ax2.set_title('Monthly Orphan Entries (Not in Buildstepoutputs)')
ax2.xaxis.set_major_locator(mdates.YearLocator())
ax2.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax2.grid(True, alpha=0.3, axis='y')

# Plot 3: Monthly orphan percentage
ax3 = axes2[2]
ax3.bar(monthly['month'], monthly['orphan_pct'], width=25, color='forestgreen', alpha=0.8)
ax3.set_ylabel('Orphan %')
ax3.set_xlabel('Date')
ax3.set_title('Monthly Orphan Percentage')
ax3.xaxis.set_major_locator(mdates.YearLocator())
ax3.xaxis.set_major_formatter(mdates.DateFormatter('%Y'))
ax3.grid(True, alpha=0.3, axis='y')

plt.tight_layout()
plt.savefig('/home/brian/Development/com/github/numtide/narwal/.data/inventory-monthly.png', dpi=150)
print("Saved: inventory-monthly.png")

# Yearly summary
df['year'] = df['day'].dt.year
yearly = df.groupby('year').agg({
    'total_entries': 'sum',
    'orphan_entries': 'sum'
}).reset_index()
yearly['orphan_pct'] = yearly['orphan_entries'] * 100.0 / yearly['total_entries']

fig3, ax = plt.subplots(figsize=(12, 6))
x = range(len(yearly))
width = 0.35
bars1 = ax.bar([i - width/2 for i in x], yearly['total_entries'] / 1e6, width, label='Total Entries (M)', color='steelblue')
bars2 = ax.bar([i + width/2 for i in x], yearly['orphan_entries'] / 1e3, width, label='Orphan Entries (K)', color='coral')
ax.set_xlabel('Year')
ax.set_ylabel('Count')
ax.set_title('Yearly Inventory Summary')
ax.set_xticks(x)
ax.set_xticklabels(yearly['year'])
ax.legend()
ax.grid(True, alpha=0.3, axis='y')

# Add orphan % labels
for i, (_, row) in enumerate(yearly.iterrows()):
    ax.annotate(f"{row['orphan_pct']:.2f}%",
                xy=(i + width/2, row['orphan_entries']/1e3),
                ha='center', va='bottom', fontsize=8)

plt.tight_layout()
plt.savefig('/home/brian/Development/com/github/numtide/narwal/.data/inventory-yearly.png', dpi=150)
print("Saved: inventory-yearly.png")

print("\nYearly Summary:")
print(yearly.to_string(index=False))
