const defaultSiteDomainSuffix = "localhost";

export const siteDomainSuffix = normalizeSiteDomainSuffix(
  import.meta.env.VITE_SITE_DOMAIN_SUFFIX ?? defaultSiteDomainSuffix,
);

export const siteDomainPlaceholder = `demo.${siteDomainSuffix}, www.${siteDomainSuffix}`;

function normalizeSiteDomainSuffix(value: string) {
  const normalized = value.trim().toLowerCase().replace(/\.$/, "");

  return normalized || defaultSiteDomainSuffix;
}
