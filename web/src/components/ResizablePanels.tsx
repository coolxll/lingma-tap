import { useState, useRef, useEffect, Children } from 'react';
import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react';
import { cn } from '@/lib/utils';

interface ResizablePanelsProps {
  children: ReactNode;
  defaultSizes: number[];
  minSizes?: number[];
  className?: string;
}

export function ResizablePanels({
  children,
  defaultSizes,
  minSizes = [],
  className,
}: ResizablePanelsProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [sizes, setSizes] = useState<number[]>(defaultSizes);
  const draggingRef = useRef<number | null>(null);
  const startXRef = useRef(0);
  const startSizesRef = useRef<number[]>([]);

  const childArray = Children.toArray(children);

  const handleMouseDown = (index: number, e: ReactMouseEvent) => {
    e.preventDefault();
    draggingRef.current = index;
    startXRef.current = e.clientX;
    startSizesRef.current = [...sizes];
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  };

  useEffect(() => {
    const handleMouseMove = (e: globalThis.MouseEvent) => {
      if (draggingRef.current === null || !containerRef.current) return;
      const containerWidth = containerRef.current.offsetWidth;
      const deltaX = e.clientX - startXRef.current;
      const deltaPercent = (deltaX / containerWidth) * 100;
      const index = draggingRef.current;
      const newSizes = [...startSizesRef.current];

      let leftSize = newSizes[index] + deltaPercent;
      let rightSize = newSizes[index + 1] - deltaPercent;
      const leftMin = minSizes[index] ? (minSizes[index] / containerWidth) * 100 : 5;
      const rightMin = minSizes[index + 1] ? (minSizes[index + 1] / containerWidth) * 100 : 5;

      if (leftSize < leftMin) {
        leftSize = leftMin;
        rightSize = startSizesRef.current[index] + startSizesRef.current[index + 1] - leftMin;
      }
      if (rightSize < rightMin) {
        rightSize = rightMin;
        leftSize = startSizesRef.current[index] + startSizesRef.current[index + 1] - rightMin;
      }

      newSizes[index] = leftSize;
      newSizes[index + 1] = rightSize;
      setSizes(newSizes);
    };

    const handleMouseUp = () => {
      draggingRef.current = null;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [minSizes]);

  const sepCount = childArray.length - 1;

  return (
    <div ref={containerRef} className={cn('flex h-full w-full', className)}>
      {childArray.map((child, index) => (
        <div key={index} className="contents">
          <div
            style={{
              width: `calc(${sizes[index]}% - ${(sepCount * 4) / childArray.length}px)`,
              flexShrink: 0,
            }}
            className="h-full overflow-hidden"
          >
            {child}
          </div>
          {index < childArray.length - 1 && (
            <div
              role="separator"
              tabIndex={0}
              onMouseDown={(e) => handleMouseDown(index, e)}
              className="h-full w-1 flex-shrink-0 cursor-col-resize bg-zinc-800 hover:bg-blue-500 active:bg-blue-600 transition-colors"
            />
          )}
        </div>
      ))}
    </div>
  );
}
