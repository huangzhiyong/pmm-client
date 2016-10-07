/*
	Copyright (c) 2016, Percona LLC and/or its affiliates. All rights reserved.

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU Affero General Public License for more details.

	You should have received a copy of the GNU Affero General Public License
	along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package pmm

import (
	"fmt"

	"github.com/go-sql-driver/mysql"
	consul "github.com/hashicorp/consul/api"
	"github.com/percona/kardianos-service"
)

// AddProxySQLMetrics add proxysql service to monitoring.
func (a *Admin) AddProxySQLMetrics(dsn string) error {
	// Check if we have already this service on Consul.
	consulSvc, err := a.getConsulService("proxysql:metrics", a.ServiceName)
	if err != nil {
		return err
	}
	if consulSvc != nil {
		return ErrDuplicate
	}

	if err := a.checkGlobalDuplicateService("proxysql:metrics", a.ServiceName); err != nil {
		return err
	}

	// Choose port.
	var port uint16
	if a.ServicePort > 0 {
		// The port is user defined.
		port, err = a.choosePort(a.ServicePort, true)
	} else {
		// Choose first port available starting the given default one.
		port, err = a.choosePort(42004, false)
	}
	if err != nil {
		return err
	}

	// Add service to Consul.
	serviceID := fmt.Sprintf("proxysql:metrics-%d", port)
	srv := consul.AgentService{
		ID:      serviceID,
		Service: "proxysql:metrics",
		Tags:    []string{fmt.Sprintf("alias_%s", a.ServiceName)},
		Port:    int(port),
	}
	reg := consul.CatalogRegistration{
		Node:    a.Config.ClientName,
		Address: a.Config.ClientAddress,
		Service: &srv,
	}
	if _, err := a.consulapi.Catalog().Register(&reg, nil); err != nil {
		return err
	}

	// Add info to Consul KV.
	d := &consul.KVPair{Key: fmt.Sprintf("%s/%s/dsn", a.Config.ClientName, serviceID),
		Value: []byte(SanitizeDSN(dsn))}
	a.consulapi.KV().Put(d, nil)

	// Install and start service via platform service manager.
	svcConfig := &service.Config{
		Name:        fmt.Sprintf("pmm-proxysql-metrics-%d", port),
		DisplayName: "PMM Prometheus proxysql_exporter",
		Description: "PMM Prometheus proxysql_exporter",
		Executable:  fmt.Sprintf("%s/proxysql_exporter", PMMBaseDir),
		Arguments:   []string{fmt.Sprintf("-web.listen-address=%s:%d", a.Config.ClientAddress, port)},
		Option:      service.KeyValue{"Environment": fmt.Sprintf("DATA_SOURCE_NAME=%s", dsn)},
	}
	if err := installService(svcConfig); err != nil {
		return err
	}

	return nil
}

// RemoveProxySQLMetrics remove proxysql service from monitoring.
func (a *Admin) RemoveProxySQLMetrics() error {
	// Check if we have this service on Consul.
	consulSvc, err := a.getConsulService("proxysql:metrics", a.ServiceName)
	if err != nil {
		return err
	}
	if consulSvc == nil {
		return ErrNoService
	}

	// Remove service from Consul.
	dereg := consul.CatalogDeregistration{
		Node:      a.Config.ClientName,
		ServiceID: consulSvc.ID,
	}
	if _, err := a.consulapi.Catalog().Deregister(&dereg, nil); err != nil {
		return err
	}

	prefix := fmt.Sprintf("%s/%s/", a.Config.ClientName, consulSvc.ID)
	a.consulapi.KV().DeleteTree(prefix, nil)

	// Stop and uninstall service.
	if err := uninstallService(fmt.Sprintf("pmm-proxysql-metrics-%d", consulSvc.Port)); err != nil {
		return err
	}

	return nil
}

// DetectProxySQL verify ProxySQL connection.
func (a *Admin) DetectProxySQL(dsnString string) error {
	dsn, err := mysql.ParseDSN(dsnString)
	if err != nil {
		return fmt.Errorf("Bad dsn %s: %s", dsnString, err)
	}

	if err := testConnection(dsn.FormatDSN()); err != nil {
		return fmt.Errorf("Cannot connect to ProxySQL using DSN %s: %s", dsnString, err)
	}

	return nil
}