pipeline {
    agent {
        docker {
            image 'golang:1.27.4-trixie'
        }
    }
    environment {
        GOCACHE = "${WORKSPACE}/.cache/go-build"
        GOPATH  = "${WORKSPACE}/.cache/go"
        GOFLAGS = '-modcacherw'
    }
    stages {
        stage('Vet') {
            steps {
                sh 'go vet ./...'
            }
        }
        stage('Test') {
            steps {
                sh 'go test -race ./...'
            }
        }
        stage('Build') {
            environment {
                CGO_ENABLED = '0'
                GOOS        = 'linux'
                GOARCH      = 'arm64'
            }
            steps {
                sh '''
                    commit=$(git rev-parse --short HEAD)
                    go build -trimpath -ldflags "-X main.version=${BUILD_NUMBER}-${commit}" -o bin/fleet-app-linux-arm64 ./cmd/fleet-app
                '''
            }
        }
    }
    post {
        success {
            archiveArtifacts artifacts: 'bin/fleet-app-linux-arm64', fingerprint: true
        }
    }
}
