import { Button } from '@datum-cloud/datum-ui/button';
import { useDataTableSearch } from '@datum-cloud/datum-ui/data-table';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { InputWithAddons } from '@datum-cloud/datum-ui/input-with-addons';
import { SearchIcon, XIcon } from 'lucide-react';

export function TableSearch({ placeholder }: { placeholder: string }) {
  const { search, setSearch, clearSearch } = useDataTableSearch();
  const value = typeof search === 'string' ? search : '';

  return (
    <div className="w-full min-w-full flex-1 rounded-md sm:max-w-3xs md:min-w-80">
      <InputWithAddons
        type="text"
        placeholder={placeholder}
        value={value}
        onChange={(e) => {
          const next = e.target.value;
          if (next === '') clearSearch();
          else setSearch(next);
        }}
        containerClassName="h-9 bg-transparent"
        className="placeholder:text-secondary text-secondary h-full bg-transparent text-xs placeholder:text-xs md:text-xs dark:text-white dark:placeholder:text-white"
        leading={
          <Icon icon={SearchIcon} size={14} className="text-icon-quaternary dark:text-white" />
        }
        trailing={
          value ? (
            <Button
              type="quaternary"
              theme="borderless"
              size="icon"
              onClick={() => clearSearch()}
              className="hover:text-destructive text-icon-quaternary size-4 p-0 hover:bg-transparent dark:text-white">
              <Icon icon={XIcon} size={14} />
              <span className="sr-only">Clear search</span>
            </Button>
          ) : undefined
        }
      />
    </div>
  );
}
