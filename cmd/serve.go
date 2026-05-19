/*
 *  Copyright (C) 2021 7Cav.us
 *  This file is part of 7Cav-API <https://github.com/7cav/api>.
 *
 *  7Cav-API is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  7Cav-API is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with 7Cav-API. If not, see <http://www.gnu.org/licenses/>.
 */

package cmd

import (
	"fmt"
	"github.com/7cav/api/servers"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launches the api servers",
	Run: func(cmd *cobra.Command, args []string) {
		// PORT is the port the HTTP gateway dials to reach the in-process gRPC server.
		// It is NOT a listen port: both the gRPC server (:10000) and HTTP gateway (:11000) listen
		// ports are hardcoded in servers/server.go. PORT must match the hardcoded gRPC port (10000)
		// for the gateway-to-gRPC dial to succeed.
		server := servers.New(fmt.Sprintf("0.0.0.0:%s", viper.GetString("port")))
		server.Start()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
