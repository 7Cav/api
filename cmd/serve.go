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
	"github.com/7cav/api/servers"
	"github.com/spf13/cobra"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launches the api server",
	Run: func(cmd *cobra.Command, args []string) {
		// The public (:11000) and internal metrics (:9090) listen ports are
		// constants in servers/server.go. The old PORT env var was only the
		// gateway's gRPC dial target; the gRPC server is gone (#134), so PORT is
		// no longer read.
		server := servers.New()
		server.Start()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
